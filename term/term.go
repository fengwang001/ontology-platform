// Package term 维护单个节点的 term/votedFor 簿记与 RequestVote 的同意/拒绝判定。
// 不依赖其他包。
package term

// Book 是单节点的投票簿：Term 当前任期，VotedFor 本任期投给的候选（-1 表示未投）。
type Book struct {
	Term     int
	VotedFor int
}

// New 返回初始簿记：任期 0，未投票。
func New() Book { return Book{Term: 0, VotedFor: -1} }

// StartElection 本节点成为候选：任期加 1，投给自己。返回新任期。
func (b *Book) StartElection(self int) int {
	b.Term++
	b.VotedFor = self
	return b.Term
}

// RequestVote 判定是否同意把票投给候选 c（请求任期 t，t >= 0 由调用方保证）。
// 同意时按规则改写簿记并返回 true；拒绝时状态不变返回 false。
func (b *Book) RequestVote(c, t int) bool {
	switch {
	case t < b.Term: // 过期任期：拒绝，状态不变
		return false
	case t > b.Term: // 更高任期：提升任期并投票
		b.Term = t
		b.VotedFor = c
		return true
	case b.VotedFor == -1 || b.VotedFor == c: // 同任期：未投或重复投同一候选
		b.VotedFor = c
		return true
	default: // 同任期已投别人：拒绝，状态不变
		return false
	}
}
