// Package request 维护数据访问申请及其审批状态（无锁）。
package request

import "strconv"

// Status 是申请状态。
type Status int

const (
	Pending Status = iota
	Approved
	Denied
)

// Request 是一条访问申请。
type Request struct {
	ID        string
	User      []byte
	Res       []byte
	Dur       int64
	CreatedAt int64
	Status    Status
}

// Expired 报告 Pending 申请在 now 是否已过期（提交时刻+P，恰等过期）。
func (q *Request) Expired(now, p int64) bool {
	return q.Status == Pending && now >= q.CreatedAt+p
}

// Book 是申请登记簿。
type Book struct {
	reqs map[string]*Request
}

// NewBook 创建空登记簿。
func NewBook() *Book { return &Book{reqs: make(map[string]*Request)} }

// Has 报告申请 id 是否出现过（含已处理/已过期）。
func (b *Book) Has(id string) bool { _, ok := b.reqs[id]; return ok }

// Get 取申请。
func (b *Book) Get(id string) *Request { return b.reqs[id] }

// Add 写入一条 Pending 申请。
func (b *Book) Add(id string, u, r []byte, dur, now int64) *Request {
	q := &Request{ID: id, User: append([]byte(nil), u...), Res: append([]byte(nil), r...),
		Dur: dur, CreatedAt: now, Status: Pending}
	b.reqs[id] = q
	return q
}

// HasPending 报告 (u,r) 是否存在尚未处理且未过期的 Pending 申请。
func (b *Book) HasPending(u, r []byte, now, p int64) bool {
	key := urKey(u, r)
	for _, q := range b.reqs {
		if q.Status == Pending && !q.Expired(now, p) && urKey(q.User, q.Res) == key {
			return true
		}
	}
	return false
}

// SetStatus 标记申请的终态。
func (b *Book) SetStatus(id string, st Status) {
	if q := b.reqs[id]; q != nil {
		q.Status = st
	}
}

func urKey(u, r []byte) string {
	return strconv.Itoa(len(u)) + ":" + string(u) + "/" + strconv.Itoa(len(r)) + ":" + string(r)
}
