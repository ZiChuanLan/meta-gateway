package observability

import "sync/atomic"

type State struct{ ready atomic.Bool }

func NewState() *State       { state := &State{}; state.ready.Store(true); return state }
func (s *State) Ready() bool { return s != nil && s.ready.Load() }
func (s *State) SetReady(ready bool) {
	if s != nil {
		s.ready.Store(ready)
	}
}
