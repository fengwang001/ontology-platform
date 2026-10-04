package lot

import (
	"errors"
	"sync"

	"ontology/plan"
	"ontology/switchrule"
)

var (
	ErrInvalidArgument = errors.New("lot: invalid argument")
	ErrUnauthorized    = errors.New("lot: unauthorized")
	ErrNotFound        = errors.New("lot: not found")
	ErrSuspended       = errors.New("lot: stream suspended")
	ErrConflictState   = errors.New("lot: state conflict")
	ErrConflictID      = errors.New("lot: lot id conflict")
	ErrCountExceedsN   = errors.New("lot: defect count exceeds sample size")
)

type Role int

const (
	RoleInspector Role = iota
	RoleManager
)

type Operator struct {
	ID   string
	Role Role
}

type StreamKey struct {
	Supplier string
	Material string
}

type Status int

const (
	Pending Status = iota
	Released
	Rejected
	Scrapped
)

type Decision int

const (
	Accept Decision = iota
	Reject
	BorderlineAccept
)

type Scheme = plan.Scheme
type Severity = plan.Severity

type Lot struct {
	ID           string
	Key          StreamKey
	N            int
	Scheme       Scheme
	Status       Status
	AtSeverity   Severity
	Reinspection bool
}

type lotRec struct {
	id         string
	key        StreamKey
	n          int
	scheme     plan.Scheme
	status     Status
	atSeverity plan.Severity
	rechecked  bool
}

type stream struct {
	engine *switchrule.Stream
	open   string // 未判定批（含复检轮）ID
}

type Inspector struct {
	mu      sync.Mutex
	table   *plan.Table
	eng     *switchrule.Engine
	streams map[StreamKey]*stream
	lots    map[string]*lotRec
}

func NewInspector(t *plan.Table) *Inspector {
	return &Inspector{
		table:   t,
		eng:     switchrule.New(t.LR()),
		streams: make(map[StreamKey]*stream),
		lots:    make(map[string]*lotRec),
	}
}

func (in *Inspector) Submit(op Operator, key StreamKey, lotID string, n int) (*Lot, error) {
	if op.ID == "" || lotID == "" || key.Supplier == "" || key.Material == "" ||
		n < 1 || n > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if op.Role != RoleInspector {
		return nil, ErrUnauthorized
	}
	st, ok := in.streams[key]
	if ok && in.eng.State(st.engine) == plan.Suspended {
		return nil, ErrSuspended
	}
	if ok && st.open != "" {
		return nil, ErrConflictState
	}
	if _, dup := in.lots[lotID]; dup {
		return nil, ErrConflictID
	}
	if !ok {
		st = &stream{engine: in.eng.NewStream()}
		in.streams[key] = st
	}
	sev := in.eng.State(st.engine)
	sc, err := in.table.SchemeFor(n, sev)
	if err != nil {
		return nil, mapPlanErr(err)
	}
	rec := &lotRec{
		id:         lotID,
		key:        key,
		n:          n,
		scheme:     sc,
		status:     Pending,
		atSeverity: sev,
	}
	in.lots[lotID] = rec
	st.open = lotID
	return snapshot(rec), nil
}

func (in *Inspector) Record(op Operator, lotID string, d int) (Decision, Status, error) {
	if op.ID == "" || lotID == "" || d < 0 {
		return 0, 0, ErrInvalidArgument
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if op.Role != RoleInspector {
		return 0, 0, ErrUnauthorized
	}
	rec, ok := in.lots[lotID]
	if !ok {
		return 0, 0, ErrNotFound
	}
	st := in.streams[rec.key]
	// 初检在暂停流上不应出现（Submit 已拦）；复检允许。
	if !rec.rechecked && in.eng.State(st.engine) == plan.Suspended {
		return 0, 0, ErrSuspended
	}
	if st.open != lotID || rec.status != Pending {
		return 0, 0, ErrConflictState
	}
	if d > rec.scheme.N {
		return 0, 0, ErrCountExceedsN
	}

	var decision Decision
	verdict := switchrule.Accept
	switch {
	case d <= rec.scheme.Ac:
		decision = Accept
		verdict = switchrule.Accept
	case d >= rec.scheme.Re:
		decision = Reject
		verdict = switchrule.Reject
	default:
		decision = BorderlineAccept
		verdict = switchrule.BorderlineAccept
	}

	if rec.rechecked {
		if decision == Reject {
			rec.status = Scrapped
		} else {
			rec.status = Released
		}
	} else {
		if decision == Reject {
			rec.status = Rejected
		} else {
			rec.status = Released
		}
		in.eng.Observe(st.engine, switchrule.Record{D: d, Verdict: verdict})
	}
	st.open = ""
	return decision, rec.status, nil
}

func (in *Inspector) Resubmit(op Operator, lotID string) error {
	if op.ID == "" || lotID == "" {
		return ErrInvalidArgument
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if op.Role != RoleInspector {
		return ErrUnauthorized
	}
	rec, ok := in.lots[lotID]
	if !ok {
		return ErrNotFound
	}
	st := in.streams[rec.key]
	if rec.status != Rejected || rec.rechecked {
		return ErrConflictState
	}
	if st.open != "" {
		return ErrConflictState
	}
	sc, err := in.table.SchemeFor(rec.n, plan.Tightened)
	if err != nil {
		return mapPlanErr(err)
	}
	rec.rechecked = true
	rec.status = Pending
	rec.scheme = sc
	st.open = rec.id
	return nil
}

func (in *Inspector) Resume(op Operator, key StreamKey) error {
	if op.ID == "" || key.Supplier == "" || key.Material == "" {
		return ErrInvalidArgument
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if op.Role != RoleManager {
		return ErrUnauthorized
	}
	st, ok := in.streams[key]
	if !ok {
		return ErrNotFound
	}
	if in.eng.State(st.engine) != plan.Suspended {
		return ErrConflictState
	}
	in.eng.Resume(st.engine)
	return nil
}

func (in *Inspector) Severity(key StreamKey) (Severity, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	st, ok := in.streams[key]
	if !ok {
		return plan.Normal, false
	}
	return in.eng.State(st.engine), true
}

func (in *Inspector) Lookup(lotID string) (*Lot, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	rec, ok := in.lots[lotID]
	if !ok {
		return nil, false
	}
	return snapshot(rec), true
}

func snapshot(rec *lotRec) *Lot {
	return &Lot{
		ID:           rec.id,
		Key:          rec.key,
		N:            rec.n,
		Scheme:       rec.scheme,
		Status:       rec.status,
		AtSeverity:   rec.atSeverity,
		Reinspection: rec.rechecked,
	}
}

func mapPlanErr(err error) error {
	if errors.Is(err, plan.ErrNoRange) {
		return ErrInvalidArgument
	}
	return ErrInvalidArgument
}
