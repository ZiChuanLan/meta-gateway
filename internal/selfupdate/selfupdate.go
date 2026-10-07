package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/buildinfo"
	"github.com/lan/meta-gateway/internal/updatecheck"
)

// Phases surfaced to the console while an update runs. After "handoff" the
// initiating process no longer owns the outcome — the successor container
// finishes the swap and the console watches /healthz for the new version.
const (
	PhaseIdle              = "idle"
	PhaseChecking          = "checking"
	PhasePulling           = "pulling"
	PhaseStartingSuccessor = "starting-successor"
	PhaseHandoff           = "handoff"
	PhaseFailed            = "failed"
)

// Errors surfaced to the API layer.
var (
	ErrUnavailable   = errors.New("self-update unavailable: docker socket not mounted")
	ErrAlreadyRuning = errors.New("self-update already in progress")
	ErrNoContainer   = errors.New("self-update requires a Docker deployment (HOSTNAME unset)")
	ErrBadTarget     = errors.New("self-update target must be a newer release tag")
	ErrTrackMismatch = errors.New("watchtower_channel_mismatch: set IMAGE_TAG to beta or latest and recreate the container before switching tracks")
)

// Update mode: compose-updater sidecar (preferred — it re-reads the deployment
// file, so env changes land too), the watchtower companion (no socket in the
// gateway, but env changes never reach the new container), direct socket
// handoff, or none (copy-command fallback).
type Mode string

const (
	ModeCompose    Mode = "compose"
	ModeWatchtower Mode = "watchtower"
	ModeSocket     Mode = "socket"
	ModeNone       Mode = "none"
)

// Status is the console-facing update state.
type Status struct {
	Target    string `json:"target,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`
	// From is the build the task started from. The console needs it to confirm a
	// tracked-tag update from another browser: the executor installs whatever the
	// tag points to, so "the version changed and is newer than this" is the only
	// honest success criterion on that path.
	From      string `json:"from,omitempty"`
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	Phase     string `json:"phase"`
	Mode      Mode   `json:"mode"`
	Error     string `json:"error,omitempty"`
	// TrackingTag / TrackingChannel describe what the executor can actually
	// install. In watchtower mode it installs whatever the tracked tag points to
	// at that moment — not the release the console named — so the console has to
	// show the tag it is really asking for.
	TrackingTag     string `json:"tracking_tag,omitempty"`
	TrackingChannel string `json:"tracking_channel,omitempty"`
	// UpdaterProject is where the compose updater runs (its project directory).
	// It is the one detail an operator needs to verify what an update will
	// touch, and it comes from the updater's own heartbeat.
	UpdaterProject string `json:"updater_project,omitempty"`
	// LastResult is the previous compose-updater run, written by the sidecar.
	// Without it a failed update leaves the old container running and no
	// explanation anywhere the operator looks.
	LastResult *ComposeUpdaterResult `json:"last_result,omitempty"`
}

// Service runs the one-click update orchestration.
type Service struct {
	socket string
	client *Client
	now    func() time.Time

	mu          sync.Mutex
	phase       string
	errStr      string
	target      string
	from        string
	started     time.Time
	journalPath string
}

func New(socket string) *Service {
	return &Service{
		socket: socket,
		client: NewClient(socket),
		now:    time.Now,
		phase:  PhaseIdle,
	}
}

// socketAvailable reports whether the direct handoff path can run: the
// Docker socket exists and the process lives in a container (HOSTNAME set).
func (s *Service) socketAvailable() bool {
	return SocketAvailable(s.socket) && OwnContainerID() != ""
}

// Mode picks the execution path: the compose updater when its sidecar is
// sharing the state volume, otherwise the watchtower companion, otherwise the
// direct socket handoff.
func (s *Service) Mode() Mode {
	if ComposeUpdaterAvailable() {
		return ModeCompose
	}
	if WatchtowerReachable() {
		return ModeWatchtower
	}
	if s.socketAvailable() {
		return ModeSocket
	}
	return ModeNone
}

// Status snapshots the current update state.
func (s *Service) Status() Status {
	// Mode() dials the watchtower companion, so it is resolved before taking the
	// lock: holding the lock across a network probe makes every other caller —
	// including the successor watchdog trying to record a failure — queue behind
	// it. On a host where the companion's name does not resolve, that is a DNS
	// timeout per poll.
	mode := s.Mode()
	// Both of these are small files on the shared volume; reading them here keeps
	// the console's poll the only place that has to know about the protocol.
	var updaterProject string
	var lastResult *ComposeUpdaterResult
	if mode == ModeCompose {
		updaterProject = ComposeUpdaterProjectDir()
		if result, ok := ComposeUpdaterLastResult(); ok {
			lastResult = &result
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	available := mode != ModeNone
	if s.phase == PhaseHandoff {
		available = true
	}
	return Status{
		Target: s.target,
		StartedAt: func() int64 {
			if s.started.IsZero() {
				return 0
			}
			return s.started.UnixMilli()
		}(),
		Available:       available,
		Running:         s.phase != PhaseIdle && s.phase != PhaseFailed,
		Phase:           s.phase,
		Mode:            mode,
		Error:           s.errStr,
		From:            s.from,
		TrackingTag:     TrackingTag(),
		TrackingChannel: TrackingChannel(),
		UpdaterProject:  updaterProject,
		LastResult:      lastResult,
	}
}

func (s *Service) expireLocked() {
	if s.phase != PhaseIdle && s.phase != PhaseFailed && !s.started.IsZero() && s.now().Sub(s.started) > 16*time.Minute {
		s.phase = PhaseFailed
		s.errStr = "update completion was not confirmed before the deadline; inspect the deployment before retrying"
		if err := s.persistLocked(); err != nil {
			log.Printf("self-update: cannot persist deadline state: %v", err)
		}
	}
}

func (s *Service) fail(err error) {
	s.setPhase(PhaseFailed, err.Error())
}

func (s *Service) setPhase(phase, errStr string) {
	s.mu.Lock()
	s.phase, s.errStr = phase, errStr
	if err := s.persistLocked(); err != nil {
		log.Printf("self-update: cannot persist phase: %v", err)
	}
	s.mu.Unlock()
	log.Printf("self-update: phase=%s err=%q", phase, errStr)
}

// Start launches the update handoff in the background. target is the release
// tag the console confirmed; the pulled image ref always comes from the
// container's own configuration, so the update can never fetch a foreign
// image.
func (s *Service) Start() error { return s.start("") }

// TrackingTag is deployment-declared for Watchtower, which cannot change tags.
func TrackingTag() string { return strings.TrimSpace(os.Getenv("SELFUPDATE_TRACK_TAG")) }

// TrackingChannel maps the tracked tag to the release channel it delivers:
// "latest" carries stable releases, "beta" carries prereleases. A pinned tag
// (e.g. "4.0.0-beta.6") names one build instead of a channel, so it maps to "".
func TrackingChannel() string {
	switch TrackingTag() {
	case "beta":
		return "beta"
	case "latest":
		return "stable"
	default:
		return ""
	}
}

// TrackedTargetAllowed reports whether the console may ask a tag-following
// executor (the compose updater or the watchtower companion) for this target.
//
// Both install the image behind the tag the deployment declares — not the release
// the console named — so the guard exists to stop the console from promising a
// build it cannot bring up.
func TrackedTargetAllowed(target string) bool {
	if !updatecheck.IsReleaseTag(target) {
		return false
	}
	switch TrackingTag() {
	case "beta":
		// The beta tag carries prereleases and the stable releases after them.
		return true
	case "latest":
		// `latest` never carries a prerelease; accepting one here would install
		// latest and report success for a build nobody asked for.
		return !strings.Contains(target, "-")
	case "":
		// Unset. The variable arrives from the deployment file, so this is a
		// container created before it existed — typically one watchtower
		// recreated from its own older config, which is exactly how a v3
		// deployment arrives at v4. Refusing here would leave the console's update
		// button permanently dead on that deployment, and the executor installs
		// the container's own tag either way; the console reports the tag it is
		// really asking for.
		return true
	default:
		// A pinned version (a supported deployment choice) or a typo: the executor
		// can only re-pull that tag, so no console-named release would be
		// installed. Refusing is the honest answer.
		return false
	}
}

// trackMismatch names both sides of the disagreement. The tagged image is the
// only thing Watchtower can install, and it is set in the deployment file — not
// in the console — so the message has to say which value to change.
func trackMismatch(target string) error {
	channel := "stable"
	if strings.Contains(target, "-") {
		channel = "beta"
	}
	return fmt.Errorf("%w (target %s is a %s build; this deployment tracks IMAGE_TAG=%s)",
		ErrTrackMismatch, target, channel, TrackingTag())
}
func (s *Service) StartTarget(target string) error {
	if !updatecheck.IsReleaseTag(target) {
		return ErrBadTarget
	}
	return s.start(target)
}
func (s *Service) start(target string) error {
	mode := s.Mode()
	if mode == ModeNone {
		return ErrUnavailable
	}
	if target != "" && mode != ModeSocket && !TrackedTargetAllowed(target) {
		return trackMismatch(target)
	}
	s.mu.Lock()
	s.expireLocked()
	if s.phase != PhaseIdle && s.phase != PhaseFailed {
		s.mu.Unlock()
		return ErrAlreadyRuning
	}
	s.phase = PhaseChecking
	s.errStr = ""
	s.target = target
	s.from = buildinfo.Version
	s.started = s.now()
	if err := s.persistLocked(); err != nil {
		s.phase = PhaseFailed
		s.errStr = "cannot persist update state; no update was started"
		message := s.errStr
		s.mu.Unlock()
		return errors.New(message)
	}
	s.mu.Unlock()
	if mode == ModeCompose {
		go func() {
			s.setPhase(PhasePulling, "")
			// The sidecar does the pulling and the recreate; this process only
			// hands over the request. Its own container is what gets replaced, so
			// nothing here can report the outcome — the successor reads the
			// updater's result file instead (Status.LastResult).
			if err := TriggerComposeUpdater(target, s.from); err != nil {
				s.fail(fmt.Errorf("compose updater: %w", err))
				return
			}
			s.setPhase(PhaseHandoff, "")
		}()
		return nil
	}
	if mode == ModeWatchtower {
		go func() {
			s.setPhase(PhasePulling, "")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := TriggerWatchtower(ctx); err != nil {
				s.fail(fmt.Errorf("watchtower trigger: %w", err))
				return
			}
			s.setPhase(PhaseHandoff, "")
		}()
		return nil
	}
	go s.handoff()
	return nil
}

// handoff runs inside the OLD container: pull, create and start the
// successor, then let go. The successor finishes the swap from its own
// cgroup.
func (s *Service) handoff() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	s.setPhase(PhaseChecking, "")
	ownID := OwnContainerID()
	self, err := s.client.InspectContainer(ctx, ownID)
	if err != nil {
		s.fail(fmt.Errorf("inspect self: %w", err))
		return
	}
	if self.Name == "" {
		s.fail(errors.New("inspect self: empty container name"))
		return
	}
	repository, _ := splitImageRef(self.Config.Image)
	if repository != "zichuanlan/meta-gateway" && repository != "docker.io/zichuanlan/meta-gateway" {
		s.fail(fmt.Errorf("refusing to update foreign image %q", self.Config.Image))
		return
	}

	s.setPhase(PhasePulling, "")
	image := self.Config.Image
	s.mu.Lock()
	target := s.target
	s.mu.Unlock()
	if target != "" {
		image = repository + ":" + strings.TrimPrefix(target, "v")
	}
	if err := s.client.PullImage(ctx, image, func(line string) {
		log.Printf("self-update: pull %s", line)
	}); err != nil {
		s.fail(fmt.Errorf("pull %s: %w", image, err))
		return
	}

	s.setPhase(PhaseStartingSuccessor, "")
	nextName := self.Name + "-next"
	// A leftover successor from a failed run blocks the name — remove it.
	_ = s.client.RemoveContainer(ctx, nextName)

	portsJSON, _ := json.Marshal(self.HostConfig.PortBindings)
	env := make([]string, 0, len(self.Config.Env)+3)
	for _, entry := range self.Config.Env {
		if target != "" && strings.HasPrefix(entry, "SELFUPDATE_TRACK_TAG=") {
			continue
		}
		if strings.HasPrefix(entry, swapEnv+"=") ||
			strings.HasPrefix(entry, portsEnv+"=") ||
			strings.HasPrefix(entry, imageEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	if target != "" {
		env = append(env, "SELFUPDATE_TRACK_TAG="+strings.TrimPrefix(target, "v"))
	}
	env = append(env,
		swapEnv+"="+self.Name,
		portsEnv+"="+string(portsJSON),
		imageEnv+"="+image,
		policyEnv+"="+self.HostConfig.RestartPolicy.Name,
	)

	nextConfig := map[string]any{
		"Image":  image,
		"Env":    env,
		"Labels": self.Config.Labels,
		"HostConfig": map[string]any{
			// No host ports here: the old container still holds them. The
			// successor orchestrates and never serves traffic itself.
			//
			// AutoRemove stays off on purpose. With it on, a successor that dies
			// without finishing the swap is deleted the moment it exits — taking
			// its logs with it — and the only observable left is "the update never
			// finished". Leaving it lets the watchdog read why; the next attempt
			// removes any leftover by name (above).
			"AutoRemove":    false,
			"RestartPolicy": map[string]any{"Name": "no"},
			"NetworkMode":   networkMode(self),
			"Binds":         self.HostConfig.Binds,
			// Carried deliberately: the successor completes the swap over the
			// Docker socket. A deployment that grants socket access through the
			// host's docker group (compose group_add) loses it here otherwise,
			// and the successor then cannot open the socket it needs.
			"GroupAdd": self.HostConfig.GroupAdd,
		},
		"Healthcheck": map[string]any{"Test": []string{"NONE"}},
	}
	if len(self.Config.Entrypoint) > 0 {
		nextConfig["Entrypoint"] = self.Config.Entrypoint
	}
	if len(self.Config.Cmd) > 0 {
		nextConfig["Cmd"] = self.Config.Cmd
	}
	if self.Config.WorkingDir != "" {
		nextConfig["WorkingDir"] = self.Config.WorkingDir
	}
	if len(self.Config.ExposedPorts) > 0 {
		// Keep exposed-port metadata only if the image declares it is needed;
		// bindings are what matter and they are deliberately absent.
		nextConfig["ExposedPorts"] = self.Config.ExposedPorts
	}
	if networks := networksConfig(self); networks != nil {
		nextConfig["NetworkingConfig"] = networks
	}
	// The orchestration container needs the same socket mounts/security
	// context even when the deployment uses HostConfig.Mounts rather than Binds.
	if self.HostConfigRaw != nil {
		host := self.HostConfigRaw
		host["PortBindings"] = nil
		host["PublishAllPorts"] = false
		host["AutoRemove"] = false
		host["RestartPolicy"] = map[string]any{"Name": "no"}
		nextConfig["HostConfig"] = host
	}
	if user, ok := self.ConfigRaw["User"]; ok {
		nextConfig["User"] = user
	}

	nextID, err := s.client.CreateContainer(ctx, nextName, nextConfig)
	if err != nil {
		s.fail(fmt.Errorf("create successor: %w", err))
		return
	}
	if err := s.client.StartContainer(ctx, nextID); err != nil {
		_ = s.client.RemoveContainer(ctx, nextID)
		s.fail(fmt.Errorf("start successor: %w", err))
		return
	}
	// Handoff complete. This process (and its container) stops when the
	// successor tears the old container down.
	s.setPhase(PhaseHandoff, "")
	go s.watchSuccessor(nextID)
}

// watchSuccessor reports a successor that dies without taking over.
//
// Nothing else can: the old container is still serving, so the console sees a
// spinner at phase=handoff forever and an update that has already failed looks
// like one that is still running. This is what a missing group_add looks like
// from the outside — the successor loses socket access, exits, and says nothing.
func (s *Service) watchSuccessor(nextID string) {
	// Its own context, not the handoff's: that one is cancelled by the deferred
	// cancel the moment handoff() returns, which is immediately — the watchdog
	// would see ctx.Done() on its first select and exit without ever looking.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.fail(errors.New("successor did not finish before the deadline"))
			return
		case <-ticker.C:
		}
		next, err := s.client.InspectContainer(ctx, nextID)
		if err == nil && next.State.Running {
			continue
		}
		// Either it exited, or it is gone (a leftover successor removed by the
		// next attempt). Confirm the swap really did not happen: the successor
		// stops THIS container first, so if we are still running it never got
		// that far. The short wait covers the moment between the stop and the
		// daemon reporting it.
		time.Sleep(2 * time.Second)
		if own, ownErr := s.client.InspectContainer(ctx, OwnContainerID()); ownErr == nil && !own.State.Running {
			return // the swap is under way; this process is about to die
		}
		detail := ""
		evidence := ""
		if err == nil {
			detail = fmt.Sprintf("exit code %d", next.State.ExitCode)
			evidence = next.State.Error
		}
		if logs, logErr := s.client.Logs(ctx, nextID, 20); logErr == nil {
			evidence += "\n" + logs
		}
		detail += safeSuccessorHint(evidence)
		_ = s.client.RemoveContainer(ctx, nextID)
		if detail != "" {
			s.fail(fmt.Errorf("successor exited without completing the swap (%s)", detail))
			return
		}
		s.fail(errors.New("successor exited without completing the swap"))
		return
	}
}

// Container output may contain credentials from a custom entrypoint. Surface
// only known diagnostic categories, never arbitrary log text in an API error.
func safeSuccessorHint(evidence string) string {
	if strings.Contains(strings.ToLower(evidence), "permission denied") {
		return "; permission denied accessing the Docker socket"
	}
	return ""
}

// SwapIfRequested runs inside the SUCCESSOR container: it tears the old
// container down, recreates the final container under the original name with
// the original ports, and starts it. Called from main before serving; on
// failure it rolls the old container back and exits non-zero.
func SwapIfRequested() {
	oldName := strings.TrimSpace(os.Getenv(swapEnv))
	if oldName == "" {
		return
	}
	image := strings.TrimSpace(os.Getenv(imageEnv))
	portsJSON := strings.TrimSpace(os.Getenv(portsEnv))
	client := NewClient(DefaultSocket)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Printf("self-update: successor taking over from %q (image %s)", oldName, image)

	if err := runSwap(ctx, client, oldName, image, portsJSON, strings.TrimSpace(os.Getenv(policyEnv))); err != nil {
		log.Printf("self-update: swap failed: %v", err)
		os.Exit(1)
	}
	log.Printf("self-update: swap complete - successor exiting")
	os.Exit(0)
}

// runSwap preserves the old container until the replacement is healthy. A
// failed replacement frees its name, then restores the exact old container.
// This restores the deployment, not arbitrary backwards-incompatible DB changes.
func runSwap(ctx context.Context, client *Client, oldName, image, _, _ string) error {
	old, err := client.InspectContainer(ctx, oldName)
	if err != nil {
		return fmt.Errorf("inspect original: %w", err)
	}
	if old.HostConfig.AutoRemove {
		return errors.New("auto-remove containers require a manual update")
	}
	if image == "" {
		image = old.Config.Image
	}
	config, host := old.ConfigRaw, old.HostConfigRaw
	if config == nil || host == nil {
		return errors.New("original container configuration is missing")
	}
	config["Image"] = image
	_, trackingTag := splitImageRef(image)
	env := make([]string, 0, len(old.Config.Env)+1)
	for _, entry := range old.Config.Env {
		if strings.HasPrefix(entry, "SELFUPDATE_TRACK_TAG=") || strings.HasPrefix(entry, swapEnv+"=") || strings.HasPrefix(entry, portsEnv+"=") || strings.HasPrefix(entry, imageEnv+"=") || strings.HasPrefix(entry, policyEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	config["Env"] = append(env, "SELFUPDATE_TRACK_TAG="+trackingTag)
	// Docker-generated hostnames identify a container, not a deployment setting.
	if hostname, ok := config["Hostname"].(string); ok && strings.HasPrefix(old.ID, hostname) {
		delete(config, "Hostname")
	}
	if health, ok := config["Healthcheck"].(map[string]any); !ok || health == nil {
		port := "4100"
		for _, entry := range old.Config.Env {
			if strings.HasPrefix(entry, "HTTP_ADDR=") {
				value := strings.TrimPrefix(entry, "HTTP_ADDR=")
				i := strings.LastIndex(value, ":")
				if i < 0 {
					return errors.New("cannot determine readiness port")
				}
				port = value[i+1:]
			}
		}
		config["Healthcheck"] = map[string]any{"Test": []string{"CMD", "curl", "--fail", "--silent", "http://127.0.0.1:" + port + "/readyz"}, "Interval": int64(time.Second), "Timeout": int64(3 * time.Second), "Retries": 30, "StartPeriod": int64(10 * time.Second)}
	}
	if health, ok := config["Healthcheck"].(map[string]any); ok {
		if test, ok := health["Test"].([]any); ok && len(test) > 0 && test[0] == "NONE" {
			return errors.New("enable a health check before using direct updates")
		}
	}
	// Reattach anonymous volumes too; merely copying Config.Volumes creates new
	// empty volumes and silently loses the data path.
	binds := append([]string(nil), old.HostConfig.Binds...)
	for _, mount := range old.Mounts {
		covered := false
		for _, bind := range binds {
			if strings.Contains(bind, ":"+mount.Destination+":") || strings.HasSuffix(bind, ":"+mount.Destination) {
				covered = true
			}
		}
		if mounts, ok := host["Mounts"].([]any); ok {
			for _, raw := range mounts {
				if m, ok := raw.(map[string]any); ok && m["Target"] == mount.Destination {
					covered = true
				}
			}
		}
		if covered {
			continue
		}
		if mount.Type != "volume" && mount.Type != "bind" {
			continue
		}
		source := mount.Source
		if mount.Type == "volume" && mount.Name != "" {
			source = mount.Name
		}
		bind := source + ":" + mount.Destination
		if !mount.RW {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}
	host["Binds"] = binds
	config["HostConfig"] = host
	config["NetworkingConfig"] = networksConfig(old)
	backupName := oldName + "-rollback"
	if err = client.StopContainer(ctx, oldName, 20); err != nil {
		return rollback(client, ctx, oldName, err)
	}
	if err = client.RenameContainer(ctx, oldName, backupName); err != nil {
		return rollback(client, ctx, oldName, err)
	}
	finalID := ""
	restore := func(cause error) error {
		recovery, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if finalID != "" {
			if e := client.RemoveContainer(recovery, finalID); e != nil {
				return fmt.Errorf("%w; cannot remove failed replacement: %v", cause, e)
			}
		}
		if e := client.RenameContainer(recovery, backupName, oldName); e != nil {
			return fmt.Errorf("%w; old container retained as %s: %v", cause, backupName, e)
		}
		if e := client.StartContainer(recovery, oldName); e != nil {
			return fmt.Errorf("%w; failed to restart original: %v", cause, e)
		}
		return cause
	}
	finalID, err = client.CreateContainer(ctx, oldName, config)
	if err != nil {
		return restore(fmt.Errorf("create replacement: %w", err))
	}
	if err = client.StartContainer(ctx, finalID); err != nil {
		return restore(fmt.Errorf("start replacement: %w", err))
	}
	ready, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err = client.waitHealthy(ready, finalID); err != nil {
		return restore(fmt.Errorf("replacement not ready: %w", err))
	}
	if err = client.RemoveContainer(ctx, backupName); err != nil {
		log.Printf("self-update: replacement healthy; old container retained at %s", backupName)
	}
	return nil
}

// rollback restarts the old container so the deployment keeps serving, then
// returns the wrapped failure.
func rollback(client *Client, _ context.Context, oldName string, err error) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	log.Printf("self-update: FAILED: %v - rolling back to the old container", err)
	if startErr := client.StartContainer(ctx, oldName); startErr != nil {
		log.Printf("self-update: ROLLBACK FAILED for %s: %v - recover with: docker compose up -d", oldName, startErr)
	}
	return err
}

func networkMode(self *Container) string {
	if self.HostConfig.NetworkMode != "" && self.HostConfig.NetworkMode != "default" {
		return self.HostConfig.NetworkMode
	}
	for name := range self.NetworkSettings.Networks {
		return name
	}
	return "default"
}

func networksConfig(self *Container) map[string]any {
	if len(self.NetworkSettings.Networks) == 0 {
		return nil
	}
	endpoints := map[string]any{}
	for name, net := range self.NetworkSettings.Networks {
		entry := map[string]any{}
		if len(net.Aliases) > 0 {
			entry["Aliases"] = net.Aliases
		}
		endpoints[name] = entry
	}
	return map[string]any{"EndpointsConfig": endpoints}
}
