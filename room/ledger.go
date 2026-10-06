package room

// ledger 维护结算期的上报，并以 O(1) 方式汇总裁决所需的统计量。
//
// 不保存任何历史事件；改报直接覆盖旧值并修正计数，
// 故其开销与历史事件数、在室人数均无关。
type ledger struct {
	votes map[string]int    // 胜者 -> 票数
	given map[string]string // 上报人 -> 当前选择的胜者
	total int
}

func newLedger() *ledger {
	return &ledger{votes: map[string]int{}, given: map[string]string{}}
}

// submit 上报或改报。改报时撤销旧票并计入新票，始终为 O(1)。
func (l *ledger) submit(user, winner string) {
	if old, ok := l.given[user]; ok {
		l.votes[old]--
		if l.votes[old] == 0 {
			delete(l.votes, old)
		}
	} else {
		l.total++
	}
	l.given[user] = winner
	l.votes[winner]++
}

func (l *ledger) reportedCount() int { return l.total }

// unanimousWinner 在已上报者全部一致且至少一票时返回胜者，否则返回空串。
func (l *ledger) unanimousWinner() string {
	if len(l.votes) != 1 || l.total == 0 {
		return ""
	}
	for w := range l.votes {
		return w
	}
	return ""
}
