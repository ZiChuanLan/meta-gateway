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
)

// Status is the console-facing update state.
type Status struct {
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	Phase     string `json:"phase"`
	Error     string `json:"error,omitempty"`
}

// Service runs the one-click update orchestration.
type Service struct {
	socket string
	client *Client
	now    func() time.Time

	mu     sync.Mutex
	phase  string
	errStr string
}

func New(socket string) *Service {
	return &Service{
		socket: socket,
		client: NewClient(socket),
		now:    time.Now,
		phase:  PhaseIdle,
	}
}

// Available reports whether a one-click update can run at all: the Docker
// socket must exist and the process must live in a container (HOSTNAME set).
func (s *Service) Available() bool {
	return SocketAvailable(s.socket) && OwnContainerID() != ""
}

// Status snapshots the current update state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		Available: s.Available(),
		Running:   s.phase != PhaseIdle && s.phase != PhaseFailed,
		Phase:     s.phase,
		Error:     s.errStr,
	}
}

func (s *Service) fail(err error) {
	s.setPhase(PhaseFailed, err.Error())
}

func (s *Service) setPhase(phase, errStr string) {
	s.mu.Lock()
	s.phase, s.errStr = phase, errStr
	s.mu.Unlock()
	log.Printf("self-update: phase=%s err=%q", phase, errStr)
}

// Start launches the update handoff in the background. target is the release
// tag the console confirmed; the pulled image ref always comes from the
// container's own configuration, so the update can never fetch a foreign
// image.
func (s *Service) Start() error {
	if !s.Available() {
		return ErrUnavailable
	}
	s.mu.Lock()
	if s.phase != PhaseIdle && s.phase != PhaseFailed {
		s.mu.Unlock()
		return ErrAlreadyRuning
	}
	s.mu.Unlock()
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
	if !strings.HasPrefix(self.Config.Image, "zichuanlan/meta-gateway") {
		s.fail(fmt.Errorf("refusing to update foreign image %q", self.Config.Image))
		return
	}

	s.setPhase(PhasePulling, "")
	image := self.Config.Image
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
		if strings.HasPrefix(entry, swapEnv+"=") ||
			strings.HasPrefix(entry, portsEnv+"=") ||
			strings.HasPrefix(entry, imageEnv+"=") {
			continue
		}
		env = append(env, entry)
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
			// AutoRemove keeps the exited successor from piling up.
			"AutoRemove":    true,
			"RestartPolicy": map[string]any{"Name": "no"},
			"NetworkMode":   networkMode(self),
			"Binds":         self.HostConfig.Binds,
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
		fatalRollback(client, ctx, oldName, err)
	}
	log.Printf("self-update: swap complete - successor exiting")
	os.Exit(0)
}

// runSwap tears the old container down and recreates the final one with the
// original name, ports and restart policy. Every failure restarts the old
// container before returning the error.
func runSwap(ctx context.Context, client *Client, oldName, image, portsJSON, policyName string) error {
	self, err := client.InspectContainer(ctx, OwnContainerID())
	if err != nil {
		return rollback(client, ctx, oldName, fmt.Errorf("inspect self: %w", err))
	}
	if image == "" {
		image = self.Config.Image
	}

	// Stop and remove the old container: frees the name and the host ports.
	if err := client.StopContainer(ctx, oldName, 20); err != nil {
		return rollback(client, ctx, oldName, fmt.Errorf("stop old: %w", err))
	}
	if err := client.RemoveContainer(ctx, oldName); err != nil {
		return rollback(client, ctx, oldName, fmt.Errorf("remove old: %w", err))
	}

	env := make([]string, 0, len(self.Config.Env))
	for _, entry := range self.Config.Env {
		if strings.HasPrefix(entry, swapEnv+"=") ||
			strings.HasPrefix(entry, portsEnv+"=") ||
			strings.HasPrefix(entry, imageEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	var portBindings map[string]any
	if portsJSON != "" {
		_ = json.Unmarshal([]byte(portsJSON), &portBindings)
	}
	finalConfig := map[string]any{
		"Image":  image,
		"Env":    env,
		"Labels": self.Config.Labels,
		"HostConfig": map[string]any{
			"Binds":         self.HostConfig.Binds,
			"PortBindings":  portBindings,
			"RestartPolicy": map[string]any{"Name": restartPolicyOrDefault(policyName)},
			"NetworkMode":   networkMode(self),
		},
		"NetworkingConfig": networksConfig(self),
	}
	if len(self.Config.Entrypoint) > 0 {
		finalConfig["Entrypoint"] = self.Config.Entrypoint
	}
	if len(self.Config.Cmd) > 0 {
		finalConfig["Cmd"] = self.Config.Cmd
	}
	if self.Config.WorkingDir != "" {
		finalConfig["WorkingDir"] = self.Config.WorkingDir
	}
	if len(self.Config.ExposedPorts) > 0 {
		finalConfig["ExposedPorts"] = self.Config.ExposedPorts
	}
	// Healthcheck intentionally omitted: the image default applies again.

	finalID, err := client.CreateContainer(ctx, oldName, finalConfig)
	if err != nil {
		return rollback(client, ctx, oldName, fmt.Errorf("create final: %w", err))
	}
	if err := client.StartContainer(ctx, finalID); err != nil {
		return rollback(client, ctx, oldName, fmt.Errorf("start final: %w", err))
	}
	// The final container runs outside this cgroup, fully independent.
	log.Printf("self-update: final container %s is up", finalID)
	return nil
}

// rollback restarts the old container so the deployment keeps serving, then
// returns the wrapped failure.
func rollback(client *Client, ctx context.Context, oldName string, err error) error {
	log.Printf("self-update: FAILED: %v - rolling back to the old container", err)
	if startErr := client.StartContainer(ctx, oldName); startErr != nil {
		log.Printf("self-update: ROLLBACK FAILED for %s: %v - recover with: docker compose up -d", oldName, startErr)
	}
	return err
}

func fatalRollback(client *Client, ctx context.Context, oldName string, err error) {
	log.Printf("self-update: FAILED: %v — rolling back to the old container", err)
	if startErr := client.StartContainer(ctx, oldName); startErr != nil {
		log.Printf("self-update: ROLLBACK FAILED for %s: %v — recover with: docker compose up -d", oldName, startErr)
	}
	os.Exit(1)
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

func restartPolicyOrDefault(name string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	return "no"
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
