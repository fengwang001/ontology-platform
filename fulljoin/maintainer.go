package fulljoin

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 非法变更种类（'+'/'-' 以外）。
var errInvalidKind = errors.New("fulljoin: invalid change kind")

type sideState struct {
	byID  map[string]*Row // 行标识 -> 行
	byKey map[string]map[*Row]struct{}
}

func newSideState() sideState {
	return sideState{
		byID:  map[string]*Row{},
		byKey: map[string]map[*Row]struct{}{},
	}
}

func (s sideState) add(r *Row) {
	s.byID[r.ID] = r
	g := s.byKey[r.Key]
	if g == nil {
		g = map[*Row]struct{}{}
		s.byKey[r.Key] = g
	}
	g[r] = struct{}{}
}

func (s sideState) remove(r *Row) {
	delete(s.byID, r.ID)
	g := s.byKey[r.Key]
	delete(g, r)
	if len(g) == 0 {
		delete(s.byKey, r.Key)
	}
}

func (s sideState) rows(key string) []*Row {
	rs := make([]*Row, 0, len(s.byKey[key]))
	for r := range s.byKey[key] {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	return rs
}

func (s sideState) snapshotByID() map[string]*Row {
	cp := make(map[string]*Row, len(s.byID))
	for id, r := range s.byID {
		cp[id] = r
	}
	return cp
}

// Maintainer 是全外连接增量维护器。
// 一个实例可被并发使用：Apply 串行化写入，View/Log/Check 并发只读。
type Maintainer struct {
	mu    sync.RWMutex
	left  sideState
	right sideState
	out   map[OutRow]struct{} // 当前物化视图（集合）
	log   []Entry             // 已提交的全部日志条目
}

// New 创建空的维护器。
func New() *Maintainer {
	return &Maintainer{
		left:  newSideState(),
		right: newSideState(),
		out:   map[OutRow]struct{}{},
	}
}

func paired(key string, l, r *Row) OutRow {
	return OutRow{Key: key, Left: l, Right: r}
}

func leftPad(key string, l *Row) OutRow { // 左行的补位：空位在右侧
	return OutRow{Key: key, Left: l}
}

func rightPad(key string, r *Row) OutRow { // 右行的补位：空位在左侧
	return OutRow{Key: key, Right: r}
}

// retract 撤回一行：必须当前存在且形态相符，否则说明增量不变量被破坏。
func (m *Maintainer) retract(e Entry) {
	if e.Kind != '-' {
		panic(fmt.Sprintf("fulljoin: internal error: retract entry kind %q", e.Kind))
	}
	cur, ok := m.out[e.Row]
	if !ok {
		panic(fmt.Sprintf("fulljoin: internal error: retract missing row %v", e.Row))
	}
	_ = cur
	if !padShapeMatches(e.Row) {
		panic(fmt.Sprintf("fulljoin: internal error: retract row shape mismatch %v", e.Row))
	}
	delete(m.out, e.Row)
	m.log = append(m.log, e)
}

// padShapeMatches 校验补位行恰好一侧为 nil；配对行两侧均非 nil。
func padShapeMatches(o OutRow) bool {
	switch {
	case o.Left != nil && o.Right != nil:
		return true
	case o.Left != nil && o.Key == o.Left.Key:
		return true
	case o.Right != nil && o.Key == o.Right.Key:
		return true
	default:
		return false
	}
}

func (m *Maintainer) emit(o OutRow) {
	m.out[o] = struct{}{}
	m.log = append(m.log, Entry{Kind: '+', Row: o})
}

// insertLeft 增量插入一条左行；crossed 表示右侧计数是否恰为零。
func (m *Maintainer) insertLeft(r *Row) {
	key := r.Key
	rights := m.right.rows(key)
	if len(rights) == 0 {
		// 右侧仍为空（含左计数 0->非零 的穿越）：该左行只能以右空位形态存在。
		m.emit(leftPad(key, r))
		return
	}
	if len(m.left.rows(key)) == 1 {
		// 左计数 0->非零 且右侧非空：先撤回右行们的左空位补足行，再输出配对行。
		for _, rr := range rights {
			m.retract(Entry{Kind: '-', Row: rightPad(key, rr)})
		}
		for _, lr := range m.left.rows(key) {
			for _, rr := range rights {
				m.emit(paired(key, lr, rr))
			}
		}
		return
	}
	// 非穿越：只输出新左行与所有右行的配对行。
	for _, rr := range rights {
		m.emit(paired(key, r, rr))
	}
}

func (m *Maintainer) insertRight(r *Row) {
	key := r.Key
	lefts := m.left.rows(key)
	if len(lefts) == 0 {
		m.emit(rightPad(key, r))
		return
	}
	if len(m.right.rows(key)) == 1 {
		for _, ll := range lefts {
			m.retract(Entry{Kind: '-', Row: leftPad(key, ll)})
		}
		for _, ll := range lefts {
			for _, rr := range m.right.rows(key) {
				m.emit(paired(key, ll, rr))
			}
		}
		return
	}
	for _, ll := range lefts {
		m.emit(paired(key, ll, r))
	}
}

func (m *Maintainer) deleteLeft(r *Row) {
	key := r.Key
	rights := m.right.rows(key)
	if len(rights) == 0 {
		// 右侧为空：撤回该左行的右空位补足行。
		m.retract(Entry{Kind: '-', Row: leftPad(key, r)})
		return
	}
	if len(m.left.rows(key)) == 1 {
		// 删除发生前左计数为 1，删后 非零->零：
		// 先撤回全部配对行，再为右行输出左空位补足行。
		for _, lr := range m.left.rows(key) {
			for _, rr := range rights {
				m.retract(Entry{Kind: '-', Row: paired(key, lr, rr)})
			}
		}
		for _, rr := range rights {
			m.emit(rightPad(key, rr))
		}
		return
	}
	for _, rr := range rights {
		m.retract(Entry{Kind: '-', Row: paired(key, r, rr)})
	}
}

func (m *Maintainer) deleteRight(r *Row) {
	key := r.Key
	lefts := m.left.rows(key)
	if len(lefts) == 0 {
		m.retract(Entry{Kind: '-', Row: rightPad(key, r)})
		return
	}
	if len(m.right.rows(key)) == 1 {
		for _, ll := range lefts {
			for _, rr := range m.right.rows(key) {
				m.retract(Entry{Kind: '-', Row: paired(key, ll, rr)})
			}
		}
		for _, ll := range lefts {
			m.emit(leftPad(key, ll))
		}
		return
	}
	for _, ll := range lefts {
		m.retract(Entry{Kind: '-', Row: paired(key, ll, r)})
	}
}

// Apply 以一个事务批应用变更：全部合法才生效，任一条非法则整批拒绝，
// 失败不改变状态与已输出日志。成功时返回本次产生的增量日志条目。
func (m *Maintainer) Apply(changes []Change) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 第一趟：在从真实状态复制出的模拟集上顺序校验整批。
	// 校验失败直接返回，不触碰真实状态与日志。
	simL, simR := m.left.snapshotByID(), m.right.snapshotByID()
	for i, c := range changes {
		if c.Kind != '+' && c.Kind != '-' {
			return nil, &ChangeError{Index: i, Change: c, Err: errInvalidKind}
		}
		if c.Row.Key == "" {
			return nil, &ChangeError{Index: i, Change: c, Err: ErrEmptyKey}
		}
		store := simL
		if c.Side == SideRight {
			store = simR
		}
		switch c.Kind {
		case '+':
			if _, exists := store[c.Row.ID]; exists {
				return nil, &ChangeError{Index: i, Change: c, Err: ErrDuplicateID}
			}
			r := c.Row
			store[c.Row.ID] = &r
		case '-':
			if _, exists := store[c.Row.ID]; !exists {
				return nil, &ChangeError{Index: i, Change: c, Err: ErrRowNotFound}
			}
			delete(store, c.Row.ID)
		}
	}

	// 第二趟：从真实状态再复制一份逐变更推进的工作集用于删除定位，
	// 与校验模拟彼此独立；插入直接采用变更携带的行。
	workL := m.left.snapshotByID()
	workR := m.right.snapshotByID()
	start := len(m.log)
	for _, c := range changes {
		switch {
		case c.Kind == '+' && c.Side == SideLeft:
			r := c.Row
			rp := &r
			m.left.add(rp)
			workL[c.Row.ID] = rp
			m.insertLeft(rp)
		case c.Kind == '+' && c.Side == SideRight:
			r := c.Row
			rp := &r
			m.right.add(rp)
			workR[c.Row.ID] = rp
			m.insertRight(rp)
		case c.Kind == '-' && c.Side == SideLeft:
			r := workL[c.Row.ID]
			m.deleteLeft(r)
			m.left.remove(r)
			delete(workL, c.Row.ID)
		case c.Kind == '-' && c.Side == SideRight:
			r := workR[c.Row.ID]
			m.deleteRight(r)
			m.right.remove(r)
			delete(workR, c.Row.ID)
		}
	}
	produced := append([]Entry(nil), m.log[start:]...)
	return produced, nil
}

// Log 返回自创建以来全部已提交条目的有序副本。
func (m *Maintainer) Log() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Entry(nil), m.log...)
}
