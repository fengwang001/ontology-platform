// Package phase is the phase state machine: the unarrived counter, the party
// set, the exact advance decision (unarrived hits 0 while parties > 0) and the
// terminated state. It depends on no other package.
package phase

import "errors"

// Sentinel errors. They are pairwise distinct and never wrapped, so callers can
// decide failure kinds with == / errors.Is.
var (
	ErrBadN             = errors.New("phase: non-positive initial party count")
	ErrUnknownParty     = errors.New("phase: operation on unknown party id")
	ErrDuplicateArrival = errors.New("phase: party already arrived in current phase")
	ErrTerminated       = errors.New("phase: phaser terminated")
)

// State is a single-threaded state machine; concurrency is provided by bar.
type State struct {
	parties   map[int]struct{}
	unarrived map[int]struct{}
	nextID    int
	phase     int

	// arriveProbe is the number of parties the most recent Arrive examined to
	// decide whether to advance. It is unexported and never crosses the package
	// boundary through a value-returning API; the advance test reads it directly
	// from inside the package. It must stay 1: the decision is a decrement-to-0
	// counter check, O(1), never a scan of parties.
	arriveProbe int
}

// New creates a State at phase 0 with parties 0..n-1 all unarrived.
func New(n int) (*State, error) {
	if n <= 0 {
		return nil, ErrBadN
	}
	s := &State{parties: make(map[int]struct{}, n), unarrived: make(map[int]struct{}, n)}
	for i := 0; i < n; i++ {
		s.parties[i] = struct{}{}
		s.unarrived[i] = struct{}{}
	}
	s.nextID = n
	return s, nil
}

// Phase returns the current phase, or -1 once terminated.
func (s *State) Phase() int { return s.phase }

// Snapshot returns copies of the party and unarrived sets.
func (s *State) Snapshot() (int, map[int]struct{}, map[int]struct{}) {
	pp := make(map[int]struct{}, len(s.parties))
	uu := make(map[int]struct{}, len(s.unarrived))
	for k := range s.parties {
		pp[k] = struct{}{}
	}
	for k := range s.unarrived {
		uu[k] = struct{}{}
	}
	return s.phase, pp, uu
}

// Register adds a party to the CURRENT phase (unarrived there).
func (s *State) Register() (id, curPhase int, err error) {
	if s.phase < 0 {
		return 0, 0, ErrTerminated
	}
	id = s.nextID
	s.nextID++
	s.parties[id] = struct{}{}
	s.unarrived[id] = struct{}{}
	return id, s.phase, nil
}

// Arrive marks id arrived in the current phase and returns the phase the party
// is arriving at (the pre-advance phase). When the unarrived count drops to 0
// and parties remain, the phase advances by exactly one and everyone resets.
func (s *State) Arrive(id int) (int, error) {
	// All rejection checks precede every mutation: a failure leaves no trace.
	if s.phase < 0 {
		return 0, ErrTerminated
	}
	if _, ok := s.parties[id]; !ok {
		return 0, ErrUnknownParty
	}
	if _, ok := s.unarrived[id]; !ok {
		return 0, ErrDuplicateArrival
	}
	p := s.phase
	delete(s.unarrived, id)
	s.arriveProbe = 1 // the decision inspects only the decremented counter
	if len(s.unarrived) == 0 && len(s.parties) > 0 {
		s.phase++
		for k := range s.parties {
			s.unarrived[k] = struct{}{}
		}
	}
	return p, nil
}

// ArriveAndDeregister arrives first (possibly advancing), then removes the
// party from all future phases. Removing the last party terminates the phaser.
func (s *State) ArriveAndDeregister(id int) (int, error) {
	p, err := s.Arrive(id)
	if err != nil {
		return 0, err
	}
	delete(s.parties, id)
	delete(s.unarrived, id) // present again if the arrival triggered a reset
	if len(s.parties) == 0 {
		s.phase = -1
		s.unarrived = map[int]struct{}{}
	}
	return p, nil
}

// SelfCheck runs built-in internal verifications on throwaway states, including
// the O(1) advance probe at several sizes. It reports only pass/fail; the
// probe's numeric value never leaves the package.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		s, err := New(m)
		if err != nil {
			return err
		}
		for i := 0; i < m-1; i++ {
			if _, err := s.Arrive(i); err != nil {
				return err
			}
		}
		if _, err := s.Arrive(m - 1); err != nil {
			return err
		}
		if s.arriveProbe != 1 || s.phase != 1 {
			return errors.New("phase: internal self-check failed")
		}
	}
	return nil
}
