package demand

import "fmt"

// loadState is the runtime state of one controllable load.
type loadState struct {
	spec LoadSpec
	// connected is true when the load is currently connected.
	connected bool
	// since is the timestamp of the last connect/disconnect transition;
	// min-on / min-off durations are measured from it.
	since int64
	// locked marks an operator lock ("keep connected"): locked loads are
	// excluded from cutting. Locking a disconnected load does not restore
	// it by itself.
	locked bool
}

// loadRegistry holds all known loads.
type loadRegistry struct {
	loads map[int]*loadState
}

func newLoadRegistry() *loadRegistry {
	return &loadRegistry{loads: make(map[int]*loadState)}
}

func (r *loadRegistry) get(id int) (*loadState, bool) {
	l, ok := r.loads[id]
	return l, ok
}

// add inserts a new load in the connected state, with its min-on timer
// starting at now. The caller validates the spec first.
func (r *loadRegistry) add(spec LoadSpec, now int64) error {
	if err := spec.validate(); err != nil {
		return err
	}
	if _, ok := r.loads[spec.ID]; ok {
		return fmt.Errorf("%w: load %d already exists", ErrStateNotAllowed, spec.ID)
	}
	r.loads[spec.ID] = &loadState{spec: spec, connected: true, since: now}
	return nil
}

// minPriority returns the smallest priority number among all loads.
// Loads carrying it are critical and never cut. Returns false when empty.
func (r *loadRegistry) minPriority() (int, bool) {
	first := true
	min := 0
	for _, l := range r.loads {
		if first || l.spec.Priority < min {
			min = l.spec.Priority
			first = false
		}
	}
	return min, !first
}
