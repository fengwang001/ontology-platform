package hospital

import "sync"

type dispenseRecord struct {
	Seq      int
	At       int64
	Patient  string
	Location string
	Qty      int
}

type batchState struct {
	drug        string
	id          string
	total       int
	stocks      map[string]int
	dispenses   []dispenseRecord
	returned    map[string]int
	dispensed   map[string]int
	dispenseSeq int
}

type recallRecord struct {
	id      string
	drug    string
	first   string
	last    string
	level   int
	issueAt int64
	active  bool
}

type drugState struct {
	batches       map[string]*batchState
	activeRecalls map[string]*recallRecord
}

type System struct {
	mu      sync.Mutex
	clock   int64
	drugs   map[string]*drugState
	recalls map[string]*recallRecord
}

func NewSystem() *System {
	return &System{
		drugs:   make(map[string]*drugState),
		recalls: make(map[string]*recallRecord),
	}
}

func (s *System) drug(id string) *drugState {
	state := s.drugs[id]
	if state == nil {
		state = &drugState{
			batches:       make(map[string]*batchState),
			activeRecalls: make(map[string]*recallRecord),
		}
		s.drugs[id] = state
	}
	return state
}

func (state *drugState) batch(id string) *batchState {
	return state.batches[id]
}
