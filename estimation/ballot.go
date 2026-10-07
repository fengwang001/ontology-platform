package estimation

// ballotBox 为一轮投票的计票簿。它在投票、撤回、离开、改角色时
// 增量维护分布计数，使得：
//   - Vote/Unvote 与自动揭示条件判断均为 O(1)，与参与者总数无关；
//   - 揭示时无需遍历任何成员或选票，代价只与牌组大小有关。
//
// 不变量：dist 只含非零项；numeric 为数值牌总张数；distinct 为
// 至少有一票的数值牌种数；votes 的键数即全体已投人数。
type ballotBox struct {
	votes    map[string]Card // 投票者 -> 其当前票
	dist     map[Card]int    // 牌 -> 票数（含特殊牌，仅非零项）
	numeric  int             // 数值牌总张数
	distinct int             // 有票的数值牌种数
}

func newBallotBox() ballotBox {
	return ballotBox{votes: map[string]Card{}, dist: map[Card]int{}}
}

func (b *ballotBox) votedCount() int { return len(b.votes) }

func (b *ballotBox) add(c Card) {
	b.dist[c]++
	if !c.Special() {
		b.numeric++
		if b.dist[c] == 1 {
			b.distinct++
		}
	}
}

func (b *ballotBox) remove(c Card) {
	n := b.dist[c] - 1
	if n == 0 {
		delete(b.dist, c)
	} else {
		b.dist[c] = n
	}
	if !c.Special() {
		b.numeric--
		if n == 0 {
			b.distinct--
		}
	}
}

// cast 投下或改投一张牌。
func (b *ballotBox) cast(user string, c Card) {
	if old, ok := b.votes[user]; ok {
		if old == c {
			return
		}
		b.remove(old)
	}
	b.votes[user] = c
	b.add(c)
}

// revoke 撤回某人的票（若存在）。
func (b *ballotBox) revoke(user string) {
	if old, ok := b.votes[user]; ok {
		delete(b.votes, user)
		b.remove(old)
	}
}

// clear 清空全部投票（新一轮或新议题）。
func (b *ballotBox) clear() {
	b.votes = map[string]Card{}
	b.dist = map[Card]int{}
	b.numeric = 0
	b.distinct = 0
}

// distribution 返回当前分布的副本（含特殊牌，仅非零项）。
func (b *ballotBox) distribution() map[Card]int {
	out := make(map[Card]int, len(b.dist))
	for c, n := range b.dist {
		out[c] = n
	}
	return out
}
