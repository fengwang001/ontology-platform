// Package ledger 实现仅追加的审计账本：每个被接受的操作追加一条
// 记录，Seq 从 1 开始连续无洞；被拒绝的操作不写入。
package ledger

import "sync"

// Kind 是账本记录的操作类别。
type Kind string

const (
	KindRegister  Kind = "register"
	KindSetPolicy Kind = "set_policy"
	KindRequest   Kind = "request"
	KindApprove   Kind = "approve"
	KindReject    Kind = "reject"
)

// Entry 是一条账本记录。
type Entry struct {
	Seq       int64
	At        int64
	Kind      Kind
	ID        string // 授权 id 或主体名；SetPolicy 为空
	Actor     string // 操作者；Register 为被登记名，SetPolicy 为空
	PolicyVer int64  // 追加时的策略版本
}

// Ledger 是并发安全的仅追加账本。
type Ledger struct {
	mu      sync.Mutex
	entries []Entry
}

// New 返回空账本。
func New() *Ledger { return &Ledger{} }

// Append 追加一条记录并返回其副本，Seq 为上一条加 1。
func (l *Ledger) Append(at int64, kind Kind, id, actor string, policyVer int64) Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Entry{
		Seq:       int64(len(l.entries)) + 1,
		At:        at,
		Kind:      kind,
		ID:        id,
		Actor:     actor,
		PolicyVer: policyVer,
	}
	l.entries = append(l.entries, e)
	return e
}

// After 返回 Seq 大于 afterSeq 的全部条目副本，按 Seq 升序。
func (l *Ledger) After(afterSeq int64) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if afterSeq < 0 {
		afterSeq = 0
	}
	if afterSeq >= int64(len(l.entries)) {
		return nil
	}
	out := make([]Entry, len(l.entries)-int(afterSeq))
	copy(out, l.entries[afterSeq:])
	return out
}

// Len 返回当前条目数（即最大 Seq）。
func (l *Ledger) Len() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.entries))
}
