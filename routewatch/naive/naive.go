// Package naive is an independently written reference model for
// differential testing. It deliberately ignores every incremental trick used
// by routewatch: after every accepted operation it rebuilds the whole route
// from scratch and reconciles publications over every stop.
package naive

import "fmt"

type Kind int

const (
	Hard Kind = iota
	Soft
)

type Stop struct {
	ID       string
	Kind     Kind
	Earliest int64
	Latest   int64
	Service  int64
}

// Config mirrors routewatch.Config with local types so the two implementations
// share no code paths.
type Config struct {
	RouteID  string
	Depot    string
	Depart   int64
	Stops    []Stop
	Travel   func(from, to string) int64
	MaxDrive int64
	Rest     int64
	Debounce int64
	Lock     int64
}

type Status string

const (
	OnTime  Status = "on_time"
	Waited  Status = "waited_then_on_time"
	Late    Status = "late"
	Skipped Status = "skipped"
)

// Row is one externally comparable stop outcome. Zero time fields mean the
// stop is skipped or canceled (both are absent from valid timelines).
type Row struct {
	ID       string
	Canceled bool
	Arrival  int64
	Start    int64
	Depart   int64
	Driving  int64
	Status   Status
	Valid    bool
}

type ETA struct {
	ID  string
	ETA int64
}

type State struct {
	Clock     int64
	Rows      []Row
	Published map[string]int64
}

type Reject int

const (
	RejectNone Reject = iota
	RejectInvalid
	RejectRollback
	RejectMissing
	RejectState
	RejectSkippedStop
	RejectOrder
)

func (r Reject) Error() string { return fmt.Sprintf("naive reject %d", int(r)) }

type opKind int

const (
	opReport opKind = iota
	opCancel
)

type op struct {
	kind    opKind
	stop    string
	arrival int64
	at      int64
}

// Model replays an operation log from scratch after every accepted op.
type Model struct {
	cfg       Config
	clock     int64
	log       []op
	reported  map[string]bool
	anchors   map[string]int64
	canceled  map[string]bool
	published map[string]int64
}

func New(cfg Config) *Model {
	m := &Model{
		cfg:       cfg,
		clock:     cfg.Depart,
		reported:  map[string]bool{},
		anchors:   map[string]int64{},
		canceled:  map[string]bool{},
		published: map[string]int64{},
	}
	m.rebuild()
	return m
}

func (m *Model) Report(stop string, arrival, at int64) (State, error) {
	return m.apply(op{kind: opReport, stop: stop, arrival: arrival, at: at})
}

func (m *Model) Cancel(stop string, at int64) (State, error) {
	return m.apply(op{kind: opCancel, stop: stop, at: at})
}

func (m *Model) State() State { return m.view() }

func (m *Model) apply(candidate op) (State, error) {
	if candidate.stop == "" || candidate.arrival < 0 || candidate.at < 0 {
		return State{}, RejectInvalid
	}
	if candidate.at < m.clock {
		return State{}, RejectRollback
	}
	idx := -1
	for i := range m.cfg.Stops {
		if m.cfg.Stops[i].ID == candidate.stop {
			idx = i
			break
		}
	}
	if idx < 0 {
		return State{}, RejectMissing
	}

	// Pre-validation needs the timeline implied by the log so far.
	rows := m.simulateAll()
	switch candidate.kind {
	case opCancel:
		if m.canceled[candidate.stop] || m.reported[candidate.stop] || !rows[idx].Valid {
			return State{}, RejectState
		}
	case opReport:
		switch {
		case m.canceled[candidate.stop]:
			return State{}, RejectState
		case m.reported[candidate.stop]:
			return State{}, RejectState
		case !rows[idx].Valid:
			return State{}, RejectSkippedStop
		}
		prevDepart := m.cfg.Depart
		for j := idx - 1; j >= 0; j-- {
			if m.reported[m.cfg.Stops[j].ID] && rows[j].Valid {
				prevDepart = rows[j].Depart
				break
			}
		}
		if candidate.arrival <= prevDepart {
			return State{}, RejectOrder
		}
	}

	m.clock = candidate.at
	m.log = append(m.log, candidate)
	if candidate.kind == opReport {
		m.reported[candidate.stop] = true
		m.anchors[candidate.stop] = candidate.arrival
	} else {
		m.canceled[candidate.stop] = true
		delete(m.anchors, candidate.stop)
	}
	m.rebuild()
	return m.view(), nil
}

// rebuild recomputes every result and reconciles publications for all stops,
// exactly as the spec describes the externally visible state.
func (m *Model) rebuild() {
	rows := m.simulateAll()
	next := map[string]int64{}
	for i := range rows {
		r := rows[i]
		if m.reported[r.ID] || r.Canceled || !r.Valid {
			continue
		}
		old, existed := m.published[r.ID]
		switch {
		case !existed:
			next[r.ID] = r.Arrival
		case r.Arrival-m.clock <= m.cfg.Lock:
			next[r.ID] = old
		case delta(r.Arrival, old) > m.cfg.Debounce:
			next[r.ID] = r.Arrival
		default:
			next[r.ID] = old
		}
	}
	m.published = next
}

// simulateAll walks the whole route from the depot. Served locations only are
// linked, so a run of skipped/canceled points jumps straight from the last
// served point using that pair's table value.
func (m *Model) simulateAll() []Row {
	rows := make([]Row, len(m.cfg.Stops))
	from := m.cfg.Depot
	ready := m.cfg.Depart
	driving := int64(0)
	for i := range m.cfg.Stops {
		s := &m.cfg.Stops[i]
		rows[i] = Row{ID: s.ID}
		if m.canceled[s.ID] {
			rows[i].Canceled = true
			rows[i].Status = Skipped
			continue
		}
		row := m.advance(s, i, from, ready, driving)
		rows[i] = row
		if row.Valid {
			from, ready, driving = s.ID, row.Depart, row.Driving
		}
	}
	return rows
}

// advance evaluates one candidate reached directly from the last served
// location; skipped candidates leave the carried served state untouched.
func (m *Model) advance(s *Stop, i int, from string, ready, drivingAtDeparture int64) Row {
	leg := m.cfg.Travel(from, s.ID)
	leave := ready
	driving := drivingAtDeparture
	if driving+leg > m.cfg.MaxDrive {
		leave += m.cfg.Rest
		driving = 0
	}
	arrival := leave + leg
	if a, ok := m.anchors[s.ID]; ok {
		arrival = a
	}
	row := Row{ID: s.ID, Arrival: arrival}
	if arrival > s.Latest && s.Kind == Hard {
		row.Status = Skipped
		return row
	}
	driving += leg
	start := arrival
	if arrival < s.Earliest {
		start = s.Earliest
	}
	depart := start + s.Service
	row.Start, row.Depart, row.Valid = start, depart, true
	switch {
	case arrival > s.Latest:
		row.Status = Late
	case start > arrival:
		row.Status = Waited
	default:
		row.Status = OnTime
	}
	if depart-arrival >= m.cfg.Rest {
		driving = 0
	}
	row.Driving = driving
	return row
}

func (m *Model) view() State {
	out := make([]Row, len(m.cfg.Stops))
	copy(out, m.simulateAll())
	pub := map[string]int64{}
	for k, v := range m.published {
		pub[k] = v
	}
	return State{Clock: m.clock, Rows: out, Published: pub}
}

func delta(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}
