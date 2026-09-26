// Package pt 表示单个参与者的投票状态，保证「票一旦投出不可更改」。
package pt

import "errors"

// ErrDuplicateVote 表示对已投票的参与者再次投票。
var ErrDuplicateVote = errors.New("pt: duplicate vote")

// Vote 是一张某参与者的票。
type Vote int

const (
	// Unvoted 尚未投票。
	Unvoted Vote = iota
	// Yes 赞成。
	Yes
	// No 反对。
	No
)

// Ballot 记录单个参与者的投票状态。
type Ballot struct {
	v Vote
}

// Cast 投出一票。已投票时返回 ErrDuplicateVote 且状态不变。
func (b *Ballot) Cast(yes bool) error {
	if b.v != Unvoted {
		return ErrDuplicateVote
	}
	if yes {
		b.v = Yes
	} else {
		b.v = No
	}
	return nil
}

// Vote 返回当前票（可能为 Unvoted）。
func (b *Ballot) Vote() Vote { return b.v }
