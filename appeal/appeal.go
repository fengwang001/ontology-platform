// Package appeal 维护申诉与多人复核投票。
//
// 结案判定是 (已投票, now) 的纯函数：先到 2 票同向即结案（至多 3 票）；
// 提交后 now >= 提交时刻 + Tmax 仍未结案的，视为按维持结案，已投的票作废。
// 本包不做拒绝次序判定（由门面层负责）。
package appeal

// Vote 是一票复核投票。Overturn 为真表示投推翻，为假表示投维持。
type Vote struct {
	Reviewer string
	Overturn bool
}

// Appeal 是一条申诉。
type Appeal struct {
	ID           string
	DecisionID   string
	By           string // 申诉人（必须是内容所属创作者）
	OrigReviewer string // 原决定的审核员（须回避）
	Submitted    int64  // 提交时刻
	Votes        []Vote // 已接受的投票（被拒的票不入账）
	Closed       bool   // 是否已因投票结案
	Overturned   bool   // 投票结案的结论：真=推翻，假=维持
}

// HasVoted 报告某审核员是否已对本申诉投过票。
func (a *Appeal) HasVoted(reviewer string) bool {
	for _, v := range a.Votes {
		if v.Reviewer == reviewer {
			return true
		}
	}
	return false
}

// Board 是申诉登记表。Tmax 为复核时限。
type Board struct {
	tmax    int64
	appeals map[string]*Appeal
}

// NewBoard 返回复核时限为 tmax 的空登记表。
func NewBoard(tmax int64) *Board {
	return &Board{tmax: tmax, appeals: make(map[string]*Appeal)}
}

// Has 报告申诉 id 是否已存在。
func (b *Board) Has(id string) bool {
	_, ok := b.appeals[id]
	return ok
}

// Get 按 id 取申诉。
func (b *Board) Get(id string) (*Appeal, bool) {
	a, ok := b.appeals[id]
	return a, ok
}

// File 登记一条新申诉（调用方保证 id 不存在）。
func (b *Board) File(a *Appeal) {
	b.appeals[a.ID] = a
}

// Resolution 返回申诉在时刻 now 的结案状态，是 now 的纯函数：
// 投票已结案则按投票结论；否则 now 达到 提交时刻+Tmax 视为按维持结案。
func (b *Board) Resolution(a *Appeal, now int64) (closed, overturned bool) {
	if a.Closed {
		return true, a.Overturned
	}
	if now >= a.Submitted+b.tmax {
		return true, false
	}
	return false, false
}

// AddVote 记录一票（调用方保证未结案、投票人合法），
// 返回是否因此结案及结案结论。先有 2 票同向即结案。
func (b *Board) AddVote(a *Appeal, reviewer string, overturn bool) (resolved, overturned bool) {
	a.Votes = append(a.Votes, Vote{Reviewer: reviewer, Overturn: overturn})
	up, down := 0, 0
	for _, v := range a.Votes {
		if v.Overturn {
			up++
		} else {
			down++
		}
	}
	switch {
	case up == 2:
		a.Closed, a.Overturned = true, true
		return true, true
	case down == 2:
		a.Closed, a.Overturned = true, false
		return true, false
	}
	return false, false
}
