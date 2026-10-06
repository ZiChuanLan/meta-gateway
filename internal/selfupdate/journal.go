package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lan/meta-gateway/internal/buildinfo"
)

// The journal contains only task identity/state, never Docker config or logs.
// The data volume survives replacement, so another browser or a restarted
// process can keep observing the same deadline instead of starting over.
type journal struct {
	Phase     string `json:"phase"`
	Target    string `json:"target"`
	StartedAt int64  `json:"started_at"`
	// From is the build the update started from. Another browser (or the same
	// one after a reload) can then tell a finished update from a running one by
	// comparing the version it sees with this, which is the only comparison the
	// watchtower path can make: its executor installs whatever the tracked tag
	// points to, not the release the console named.
	From string `json:"from"`
}

func NewWithDataDir(socket, dataDir string) *Service {
	s := New(socket)
	if dataDir == "" {
		return s
	}
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		s.phase = PhaseFailed
		s.errStr = "cannot resolve update journal directory"
		return s
	}
	s.journalPath = filepath.Join(dir, "self-update-state.json")
	raw, err := os.ReadFile(s.journalPath)
	if os.IsNotExist(err) {
		return s
	}
	var record journal
	if err != nil || json.Unmarshal(raw, &record) != nil {
		s.phase = PhaseFailed
		s.errStr = "cannot read the previous update state; inspect the deployment before retrying"
		return s
	}
	s.target = record.Target
	s.from = record.From
	if record.StartedAt > 0 {
		s.started = time.UnixMilli(record.StartedAt)
	}
	if record.Target != "" && record.Target == buildinfo.Version {
		s.phase = PhaseIdle
	} else if record.Phase == PhaseFailed {
		s.phase = PhaseFailed
		s.errStr = "the previous update failed; inspect the deployment logs"
	} else if record.Phase != PhaseIdle && record.Target != "" && record.StartedAt > 0 {
		// An external executor may still be working after this process restarts.
		s.phase = PhaseHandoff
		s.expireLocked()
	}
	return s
}

func (s *Service) persistLocked() error {
	if s.journalPath == "" {
		return nil
	}
	started := int64(0)
	if !s.started.IsZero() {
		started = s.started.UnixMilli()
	}
	raw, err := json.Marshal(journal{Phase: s.phase, Target: s.target, StartedAt: started, From: s.from})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.journalPath), ".self-update-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.journalPath)
}
