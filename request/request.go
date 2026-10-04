package request

import "fmt"

type Status int

const (
	Pending Status = iota
	Approved
	Denied
)

// Key 是 (主体, 数据集) 对。
type Key struct {
	U, R string
}

// Request 是一条访问申请。
type Request struct {
	ID     string
	U, R   string
	Dur    int64
	At     int64 // 提交时刻
	Status Status
}

// ExpiredAt 报告申请在时刻 t 是否已过期（恰等即过期），仅对 Pending 有意义。
func (q *Request) ExpiredAt(t, p int64) bool {
	return q.Status == Pending && t >= q.At+p
}

// Store 是申请存储，外加 (u,r) 的 Pending 索引。
type Store struct {
	byID     map[string]*Request
	pending  map[Key]string
	validity int64 // 申请有效期 P
}

func NewStore(p int64) *Store {
	return &Store{byID: map[string]*Request{}, pending: map[Key]string{}, validity: p}
}

// Add 登记新申请。调用方负责保证 id 不重复。
func (s *Store) Add(id, u, r string, dur, now int64) {
	q := &Request{ID: id, U: u, R: r, Dur: dur, At: now, Status: Pending}
	s.byID[id] = q
	s.pending[Key{u, r}] = id
}

// Get 返回申请（含已处理者）。
func (s *Store) Get(id string) *Request { return s.byID[id] }

// HasPending 报告 (u,r) 在时刻 t 是否存在未处理且未过期的 Pending 申请。
func (s *Store) HasPending(u, r string, t int64) bool {
	id, ok := s.pending[Key{u, r}]
	if !ok {
		return false
	}
	q := s.byID[id]
	return q != nil && q.Status == Pending && !q.ExpiredAt(t, s.validity)
}

// Mark 终结一条 Pending 申请并清理其 Pending 索引。
func (s *Store) Mark(q *Request, st Status) {
	q.Status = st
	if s.pending[Key{q.U, q.R}] == q.ID {
		delete(s.pending, Key{q.U, q.R})
	}
}

func (st Status) String() string {
	switch st {
	case Pending:
		return "Pending"
	case Approved:
		return "Approved"
	case Denied:
		return "Denied"
	default:
		return fmt.Sprintf("Status(%d)", int(st))
	}
}
