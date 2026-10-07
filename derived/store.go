package derived

import "sync"

// Logger receives one record per processing unit.
type Logger interface {
	Log(rec ChangeRecord)
}

// FailHook decides whether a downstream index update fails. A failing update
// aborts and rolls back the whole processing unit.
type FailHook func(declName, downstreamID string) bool

// state is the private mutable snapshot of the store.
type state struct {
	objects map[string]*Object
	links   map[linkKey]struct{}
	// outAdj[from][typ] = set of targets.
	outAdj map[string]map[string]map[string]struct{}
	// inAdj[to][typ] = set of sources pointing at to.
	inAdj map[string]map[string]map[string]struct{}

	declarations map[string]*Declaration
	declByLink   map[string][]*Declaration
}

// Store is the derived-index subsystem. All operations are strict-serializable
// processing units: mutations build a deep-copied candidate snapshot and only
// publish it after every dependent entry update succeeds.
type Store struct {
	mu     sync.RWMutex
	cur    *state
	logger Logger

	// FailHook, when set, injects downstream update failures for tests.
	FailHook FailHook
}

// NewStore creates an empty subsystem bound to the given logger (may be nil).
func NewStore(logger Logger) *Store {
	return &Store{cur: newState(), logger: logger}
}

func newState() *state {
	return &state{
		objects:      map[string]*Object{},
		links:        map[linkKey]struct{}{},
		outAdj:       map[string]map[string]map[string]struct{}{},
		inAdj:        map[string]map[string]map[string]struct{}{},
		declarations: map[string]*Declaration{},
		declByLink:   map[string][]*Declaration{},
	}
}

// ChangeResult reports one committed or rolled-back processing unit.
type ChangeResult struct {
	Committed bool
	Affected  []string
	Entries   []Entry
	Err       *IndexError
}

// clone builds a deep copy of the snapshot for candidate application.
func (st *state) clone() *state {
	c := newState()
	for id, o := range st.objects {
		props := make(map[string]Value, len(o.Properties))
		for k, v := range o.Properties {
			props[k] = v
		}
		c.objects[id] = &Object{ID: o.ID, Type: o.Type, Properties: props}
	}
	for k := range st.links {
		c.links[k] = struct{}{}
	}
	cloneAdj := func(dst, src map[string]map[string]map[string]struct{}) {
		for id, byType := range src {
			dst[id] = map[string]map[string]struct{}{}
			for typ, set0 := range byType {
				set := make(map[string]struct{}, len(set0))
				for x := range set0 {
					set[x] = struct{}{}
				}
				dst[id][typ] = set
			}
		}
	}
	cloneAdj(c.outAdj, st.outAdj)
	cloneAdj(c.inAdj, st.inAdj)
	for name, d := range st.declarations {
		cp := *d
		c.declarations[name] = &cp
	}
	for typ, ds := range st.declByLink {
		c.declByLink[typ] = append(c.declByLink[typ], ds...)
	}
	return c
}

func (st *state) addLink(from, to, typ string) {
	k := linkKey{from, typ, to}
	if _, ok := st.links[k]; ok {
		return
	}
	st.links[k] = struct{}{}
	if st.outAdj[from] == nil {
		st.outAdj[from] = map[string]map[string]struct{}{}
	}
	if st.outAdj[from][typ] == nil {
		st.outAdj[from][typ] = map[string]struct{}{}
	}
	st.outAdj[from][typ][to] = struct{}{}
	if st.inAdj[to] == nil {
		st.inAdj[to] = map[string]map[string]struct{}{}
	}
	if st.inAdj[to][typ] == nil {
		st.inAdj[to][typ] = map[string]struct{}{}
	}
	st.inAdj[to][typ][from] = struct{}{}
}

func (st *state) removeLink(from, to, typ string) {
	k := linkKey{from, typ, to}
	if _, ok := st.links[k]; !ok {
		return
	}
	delete(st.links, k)
	if set := st.outAdj[from][typ]; set != nil {
		delete(set, to)
		if len(set) == 0 {
			delete(st.outAdj[from], typ)
		}
	}
	if set := st.inAdj[to][typ]; set != nil {
		delete(set, from)
		if len(set) == 0 {
			delete(st.inAdj[to], typ)
		}
	}
}

// removeIncidentLinks drops every adjacency entry involving id.
func (st *state) removeIncidentLinks(id string) {
	if byType, ok := st.outAdj[id]; ok {
		for typ, targets := range byType {
			for t := range targets {
				if set := st.inAdj[t][typ]; set != nil {
					delete(set, id)
					if len(set) == 0 {
						delete(st.inAdj[t], typ)
					}
				}
			}
		}
		delete(st.outAdj, id)
	}
	if byType, ok := st.inAdj[id]; ok {
		for typ, sources := range byType {
			for f := range sources {
				if set := st.outAdj[f][typ]; set != nil {
					delete(set, id)
					if len(set) == 0 {
						delete(st.outAdj[f], typ)
					}
				}
			}
		}
		delete(st.inAdj, id)
	}
}

func (s *Store) log(rec *ChangeRecord) {
	if s.logger != nil {
		s.logger.Log(*rec)
	}
}
