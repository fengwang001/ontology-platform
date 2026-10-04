package count

import (
	"sync"

	"ontology/adjust"
)

// Engine 管理盘点任务与库位阶段机。
type Engine struct {
	db *adjust.DB
	mu sync.Mutex

	// tasks 保存所有曾开启的任务；closed 后记录保留，库位解除占用。
	tasks map[string]*taskState
	// busy 记录每个库位当前所属的未关闭任务。
	busy map[string]string

	// scanned 为非导出计数器：Submit 中扫描移动流水的条数。
	// 两次实盘之差由累计净移动量 mv 的快照直接校正，恒为 0。
	scanned int
}

type taskState struct {
	closed bool
	locs   map[string]*locState
}

type locState struct {
	phase    Phase
	counters [3][]byte // 初盘/复盘/三盘人
	c1       int64
	c2       int64
	m1       int64 // 初盘时的累计净移动量快照
	diff     int64 // Pending 时的申请差值
}

func New(db *adjust.DB) *Engine {
	return &Engine{db: db, tasks: make(map[string]*taskState), busy: make(map[string]string)}
}

// Lim 为审批金额上限（分），超过它的批准申请需 Senior 权限。
func (e *Engine) Lim() int64 { return e.db.Lim() }

func (e *Engine) Open(task []byte, locs [][]byte) error {
	if !validID(task) || len(locs) < 1 || len(locs) > 1000 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(locs))
	for _, l := range locs {
		if !validID(l) || seen[string(l)] {
			return ErrInvalid
		}
		seen[string(l)] = true
	}
	for _, l := range locs {
		if !e.db.Has(l) {
			return ErrNotFound
		}
	}
	key := string(task)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tasks[key]; ok {
		return ErrConflict
	}
	for _, l := range locs {
		if _, ok := e.busy[string(l)]; ok {
			return ErrConflict
		}
	}
	t := &taskState{locs: make(map[string]*locState, len(locs))}
	for _, l := range locs {
		k := string(l)
		t.locs[k] = &locState{phase: First}
		e.busy[k] = key
	}
	e.tasks[key] = t
	return nil
}

func (e *Engine) Submit(task, loc []byte, counted int64, counter []byte) error {
	if !validID(task) || !validID(loc) || !validID(counter) ||
		counted < 0 || counted > 1_000_000_000 {
		return ErrInvalid
	}
	tkey := string(task)
	lkey := string(loc)
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[tkey]
	if !ok {
		return ErrNotFound
	}
	if t.closed {
		return ErrState
	}
	s, ok := t.locs[lkey]
	if !ok {
		return ErrNotFound
	}
	if s.phase == Pending || s.phase == Done {
		return ErrState
	}
	// 锁序 count.mu -> db.mu：快照判定与容差内落账必须原子，
	// 否则并发 Move 会插在 View 与 SetCounted 之间而被覆盖。
	e.db.Lock()
	defer e.db.Unlock()
	book, mv, _, err := e.db.SnapshotLocked(loc)
	if err != nil {
		return mapErr(err)
	}

	diff := counted - book
	tol := e.db.Tabs()
	if p := book * e.db.Tpct() / 100; p > tol {
		tol = p
	}
	within := abs64(diff) <= tol

	switch s.phase {
	case First:
		s.counters[0] = append([]byte(nil), counter...)
		if within {
			s.phase = Done
			return mapErr(e.db.SetCountedLocked(loc, counted))
		}
		s.c1, s.m1 = counted, mv
		s.phase = Second
		return nil
	case Second:
		if string(s.counters[0]) == string(counter) {
			return ErrRotate
		}
		s.counters[1] = append([]byte(nil), counter...)
		if within {
			s.phase = Done
			return mapErr(e.db.SetCountedLocked(loc, counted))
		}
		// 一致性判定只比较 mv 快照，不扫描任何流水条目。
		if counted-s.c1 == mv-s.m1 {
			s.diff = diff
			s.phase = Pending
			return nil
		}
		s.c2 = counted
		s.phase = Third
		return nil
	default: // Third
		if string(s.counters[0]) == string(counter) ||
			string(s.counters[1]) == string(counter) {
			return ErrRotate
		}
		s.counters[2] = append([]byte(nil), counter...)
		if within {
			s.phase = Done
			return mapErr(e.db.SetCountedLocked(loc, counted))
		}
		s.diff = diff
		s.phase = Pending
		return nil
	}
}

func (e *Engine) Close(task []byte) error {
	if !validID(task) {
		return ErrInvalid
	}
	key := string(task)
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[key]
	if !ok {
		return ErrNotFound
	}
	if t.closed {
		return ErrState
	}
	for _, s := range t.locs {
		if s.phase != Done {
			return ErrState
		}
	}
	t.closed = true
	for lk := range t.locs {
		delete(e.busy, lk)
	}
	return nil
}

func (e *Engine) Phase(task, loc []byte) (Phase, error) {
	if !validID(task) || !validID(loc) {
		return Done, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[string(task)]
	if !ok {
		return Done, ErrNotFound
	}
	s, ok := t.locs[string(loc)]
	if !ok {
		return Done, ErrNotFound
	}
	return s.phase, nil
}

func (e *Engine) Diff(task, loc []byte) (int64, error) {
	if !validID(task) || !validID(loc) {
		return 0, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[string(task)]
	if !ok {
		return 0, ErrNotFound
	}
	s, ok := t.locs[string(loc)]
	if !ok {
		return 0, ErrNotFound
	}
	if s.phase != Pending {
		return 0, ErrState
	}
	return s.diff, nil
}

// Pending 供 authz 读取待审批申请：各轮盘点人、申请差值与当前单价。
func (e *Engine) Pending(task, loc []byte) (counters [][]byte, diff, price int64, err error) {
	if !validID(task) || !validID(loc) {
		return nil, 0, 0, ErrInvalid
	}
	_, _, price, verr := e.db.View(loc)
	if verr != nil {
		return nil, 0, 0, mapErr(verr)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[string(task)]
	if !ok {
		return nil, 0, 0, ErrNotFound
	}
	if t.closed {
		return nil, 0, 0, ErrState
	}
	s, ok := t.locs[string(loc)]
	if !ok {
		return nil, 0, 0, ErrNotFound
	}
	if s.phase != Pending {
		return nil, 0, 0, ErrState
	}
	for _, c := range s.counters {
		if len(c) > 0 {
			counters = append(counters, append([]byte(nil), c...))
		}
	}
	return counters, s.diff, price, nil
}

// CommitApproval 供 authz 落结论：approve 时按差值落账（账面可能已被
// Pending 期间的 Move 改变，因此绝不能置为实盘数），reject 时不调整。
func (e *Engine) CommitApproval(task, loc []byte, approve bool) error {
	if !validID(task) || !validID(loc) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[string(task)]
	if !ok {
		return ErrNotFound
	}
	if t.closed {
		return ErrState
	}
	s, ok := t.locs[string(loc)]
	if !ok {
		return ErrNotFound
	}
	if s.phase != Pending {
		return ErrState
	}
	if approve {
		e.db.Lock()
		err := e.db.ApplyDiffLocked(loc, s.diff)
		e.db.Unlock()
		if err != nil {
			return mapErr(err)
		}
	}
	s.phase = Done
	s.diff = 0
	return nil
}

func validID(id []byte) bool {
	return len(id) >= 1 && len(id) <= 32
}

func mapErr(err error) error {
	switch err {
	case adjust.ErrInvalid:
		return ErrInvalid
	case adjust.ErrNotFound:
		return ErrNotFound
	case adjust.ErrStock:
		return ErrStock
	default:
		return err
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
