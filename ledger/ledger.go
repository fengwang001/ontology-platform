// Package ledger 提供只追加的审计账本。
package ledger

// Kind 是账本条目对应的操作类型。
type Kind string

const (
	KindRegister  Kind = "Register"
	KindSetPolicy Kind = "SetPolicy"
	KindRequest   Kind = "Request"
	KindApprove   Kind = "Approve"
	KindReject    Kind = "Reject"
)

// Entry 是一条审计记录，Seq 从 1 连续无洞。
type Entry struct {
	Seq       int64
	At        int64
	Kind      Kind
	ID        string
	Actor     string
	PolicyVer int64
}

// Ledger 是只追加账本，由调用方保证串行访问。
type Ledger struct {
	entries []Entry
}

// Append 追加一条记录并返回其副本。
func (l *Ledger) Append(at int64, kind Kind, id, actor string, ver int64) Entry {
	e := Entry{
		Seq:       int64(len(l.entries)) + 1,
		At:        at,
		Kind:      kind,
		ID:        id,
		Actor:     actor,
		PolicyVer: ver,
	}
	l.entries = append(l.entries, e)
	return e
}

// After 返回 Seq 大于 afterSeq 的条目副本。
func (l *Ledger) After(afterSeq int64) []Entry {
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

// Len 返回已追加的条目数。
func (l *Ledger) Len() int { return len(l.entries) }
