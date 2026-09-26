// Package term 维护单个节点的任期与投票簿记，
// 并给出 RequestVote 的同意/拒绝判定。不依赖其他包。
package term

// Book 是单节点的簿记：当前任期 term 与本任期投给的候选 votedFor（-1 表示未投）。
type Book struct {
	term     int
	votedFor int
}

// New 返回初始簿记：term=0，votedFor=-1。
func New() Book { return Book{term: 0, votedFor: -1} }

// Term 返回当前任期。
func (b *Book) Term() int { return b.term }

// VotedFor 返回本任期投给的候选，-1 表示未投。
func (b *Book) VotedFor() int { return b.votedFor }

// StartElection 使本节点成为候选：任期加 1 并投给自己。
func (b *Book) StartElection(self int) {
	b.term++
	b.votedFor = self
}

// RequestVote 判定是否同意把票投给候选 c（请求任期 t），同意则落账。
// t < term 拒绝；t > term 提升任期并投票；t == term 时仅在未投或已投 c 时同意。
func (b *Book) RequestVote(c, t int) bool {
	switch {
	case t < b.term:
		return false
	case t > b.term:
		b.term = t
		b.votedFor = c
		return true
	default: // t == b.term
		if b.votedFor == -1 || b.votedFor == c {
			b.votedFor = c
			return true
		}
		return false
	}
}
