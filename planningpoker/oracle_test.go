package planningpoker

// oracle 是完全独立于实现的朴素参照模型，直接按题目规格文字维护状态。
// 所有结论都可由成员表与投票表 O(N) 扫描得出，用于与实现做差分对照。

type oPhase int

const (
	oIdle oPhase = iota
	oVoting
	oRevealed
	oEnded
)

type oVote struct {
	value   int
	special bool
	name    string
}

type oDist struct {
	card  Card
	count int
}

type oResult struct {
	round        int
	at           int64
	dist         []oDist
	numericVotes int
	category     ResultCategory
	value        int
	final        bool
	reason       string
}

type oracle struct {
	host        string
	cards       []int
	roundLimit  int
	roundLength int64
	auto        bool

	lastNow int64
	phase   oPhase
	members map[string]Role
	votes   map[string]oVote
	// revealedVoters 为揭示时刻已投投票者集合快照。
	revealedVoters map[string]bool
	round          int
	startAt        int64
	result         *oResult
}

func newOracle(host string, cards []int, r, t int, auto bool, now int64) *oracle {
	return &oracle{
		host: host, cards: append([]int(nil), cards...),
		roundLimit: r, roundLength: int64(t), auto: auto,
		lastNow: now, members: map[string]Role{}, votes: map[string]oVote{},
		revealedVoters: map[string]bool{}, phase: oIdle,
	}
}

type oErr string

const (
	oOK        oErr = ""
	oInvalid   oErr = "invalid"
	oRewind    oErr = "rewind"
	oNotMember oErr = "notmember"
	oForbidden oErr = "forbidden"
	oState     oErr = "state"
	oExists    oErr = "exists"
	oNoVotes   oErr = "novotes"
)

func (o *oracle) validNumeric(v int) bool {
	for _, c := range o.cards {
		if c == v {
			return true
		}
	}
	return false
}

func (o *oracle) validCard(v int, special bool) bool {
	if special {
		return (v == CardUncertain.Value) || (v == CardBreak.Value)
	}
	return o.validNumeric(v)
}

// presentVoters 朴素枚举全体在室投票者。
func (o *oracle) presentVoters() []string {
	var out []string
	for u, role := range o.members {
		if role == RoleVoter {
			out = append(out, u)
		}
	}
	return out
}

func (o *oracle) votedCount() int {
	n := 0
	for u := range o.members {
		if o.members[u] == RoleVoter {
			if _, ok := o.votes[u]; ok {
				n++
			}
		}
	}
	return n
}

// pre 执行时钟检查与惰性到期。返回非空错误码时操作必须立刻拒绝。
func (o *oracle) pre(now int64) oErr {
	if now < o.lastNow {
		return oRewind
	}
	if o.phase == oVoting && now >= o.startAt+o.roundLength {
		o.reveal(o.startAt + o.roundLength)
	}
	return oOK
}

func (o *oracle) accept(now int64) { o.lastNow = now }

// reveal 按规格逐条判定结果。
func (o *oracle) reveal(at int64) *oResult {
	voters := o.presentVoters()
	o.revealedVoters = map[string]bool{}
	// 收集数值牌票（按牌组位置排序）与全牌分布。
	counts := map[int]int{}
	specialCounts := map[int]int{}
	for _, u := range voters {
		v, ok := o.votes[u]
		if !ok {
			continue
		}
		o.revealedVoters[u] = true
		if v.special {
			specialCounts[v.value]++
		} else {
			counts[v.value]++
		}
	}
	dist := make([]oDist, 0, len(o.cards)+2)
	for _, val := range o.cards {
		dist = append(dist, oDist{card: Card{Value: val}, count: counts[val]})
	}
	dist = append(dist,
		oDist{card: CardUncertain, count: specialCounts[CardUncertain.Value]},
		oDist{card: CardBreak, count: specialCounts[CardBreak.Value]},
	)
	numeric := 0
	minIdx, maxIdx := -1, -1
	for i, val := range o.cards {
		if counts[val] > 0 {
			if minIdx == -1 {
				minIdx = i
			}
			maxIdx = i
			numeric += counts[val]
		}
	}

	r := &oResult{round: o.round, at: at, dist: dist, numericVotes: numeric}
	finalRound := o.round >= o.roundLimit

	switch {
	case numeric == 0:
		if finalRound {
			r.category, r.final, r.reason = ResultTerminalNoResult, true, "no numeric votes; rounds exhausted"
			o.phase = oEnded
		} else {
			r.category, r.final, r.reason = ResultNoValidVotes, false, "no numeric votes; revote allowed"
			o.phase = oRevealed
		}
	case minIdx == maxIdx:
		r.category, r.value, r.final = ResultConsensus, o.cards[minIdx], true
		r.reason = "all numeric votes identical"
		o.phase = oEnded
	case maxIdx-minIdx == 1:
		r.category, r.value, r.final = ResultConverged, o.cards[maxIdx], true
		r.reason = "min and max adjacent in deck; take larger"
		o.phase = oEnded
	case !finalRound:
		r.category, r.final, r.reason = ResultDiverged, false, "gap > 1 deck position; rounds remain"
		o.phase = oRevealed
	default:
		// 下中位：排序后第 ceil(m/2) 张。
		target := (numeric - 1) / 2
		seen := 0
		for _, val := range o.cards {
			if seen+counts[val] > target {
				r.value = val
				break
			}
			seen += counts[val]
		}
		r.category, r.final = ResultForcedValue, true
		r.reason = "diverged at limit; lower median forced"
		o.phase = oEnded
	}
	o.result = r
	return r
}

func (o *oracle) maybeAuto(now int64) *oResult {
	if o.phase != oVoting || !o.auto {
		return nil
	}
	vp := len(o.presentVoters())
	if vp >= 1 && o.votedCount() == vp {
		return o.reveal(now)
	}
	return nil
}

func (o *oracle) isHost(u string) bool { return u == o.host }

func (o *oracle) join(u string, role Role, now int64) (*oResult, oErr) {
	if u == "" || (role != RoleVoter && role != RoleObserver) {
		return nil, oInvalid
	}
	if e := o.pre(now); e != oOK {
		return nil, e
	}
	if _, ok := o.members[u]; ok {
		return nil, oExists
	}
	o.members[u] = role
	o.accept(now)
	return o.maybeAuto(now), oOK
}

func (o *oracle) leave(u string, now int64) (*oResult, oErr) {
	if u == "" {
		return nil, oInvalid
	}
	if e := o.pre(now); e != oOK {
		return nil, e
	}
	if _, ok := o.members[u]; !ok {
		return nil, oNotMember
	}
	if o.phase == oVoting && o.members[u] == RoleVoter {
		delete(o.votes, u)
	}
	delete(o.members, u)
	// 离开即撤销且不再计入：揭示快照中也移除该席位。同名之后重新
	// 加入视为新席位，不得继承旧快照计数（实现侧以加入代次区分）。
	delete(o.revealedVoters, u)
	o.accept(now)
	return o.maybeAuto(now), oOK
}

func (o *oracle) setRole(u string, role Role, now int64) (*oResult, oErr) {
	if u == "" || (role != RoleVoter && role != RoleObserver) {
		return nil, oInvalid
	}
	if e := o.pre(now); e != oOK {
		return nil, e
	}
	cur, ok := o.members[u]
	if !ok {
		return nil, oNotMember
	}
	if cur != role {
		if o.phase == oVoting && cur == RoleVoter && role == RoleObserver {
			delete(o.votes, u)
		}
		if cur == RoleVoter && role == RoleObserver {
			delete(o.revealedVoters, u)
		}
		o.members[u] = role
	}
	o.accept(now)
	return o.maybeAuto(now), oOK
}

func (o *oracle) start(u string, now int64) oErr {
	if u == "" {
		return oInvalid
	}
	if e := o.pre(now); e != oOK {
		return e
	}
	if !o.isHost(u) {
		if _, ok := o.members[u]; !ok {
			return oNotMember
		}
		return oForbidden
	}
	if o.phase != oIdle && o.phase != oEnded {
		return oState
	}
	o.votes = map[string]oVote{}
	o.round = 1
	o.startAt = now
	o.phase = oVoting
	o.result = nil
	o.accept(now)
	o.maybeAuto(now)
	return oOK
}

func (o *oracle) vote(u string, v int, special bool, now int64) (*oResult, oErr) {
	if !o.validCard(v, special) {
		return nil, oInvalid
	}
	if e := o.pre(now); e != oOK {
		return nil, e
	}
	role, ok := o.members[u]
	if !ok {
		return nil, oNotMember
	}
	if role != RoleVoter {
		return nil, oForbidden
	}
	if o.phase != oVoting {
		return nil, oState
	}
	name := ""
	if special {
		if v == CardUncertain.Value {
			name = CardUncertain.Name
		} else {
			name = CardBreak.Name
		}
	}
	o.votes[u] = oVote{value: v, special: special, name: name}
	o.accept(now)
	return o.maybeAuto(now), oOK
}

func (o *oracle) unvote(u string, now int64) (*oResult, oErr) {
	if e := o.pre(now); e != oOK {
		return nil, e
	}
	role, ok := o.members[u]
	if !ok {
		return nil, oNotMember
	}
	if role != RoleVoter {
		return nil, oForbidden
	}
	if o.phase != oVoting {
		return nil, oState
	}
	delete(o.votes, u)
	o.accept(now)
	return nil, oOK
}

func (o *oracle) revealOp(u string, now int64) (*oResult, oErr) {
	if u == "" {
		return nil, oInvalid
	}
	if e := o.pre(now); e != oOK {
		return nil, e
	}
	if !o.isHost(u) {
		if _, ok := o.members[u]; !ok {
			return nil, oNotMember
		}
		return nil, oForbidden
	}
	if o.phase != oVoting {
		return nil, oState
	}
	if o.votedCount() == 0 {
		return nil, oNoVotes
	}
	o.accept(now)
	return o.reveal(now), oOK
}

func (o *oracle) revote(u string, now int64) oErr {
	if u == "" {
		return oInvalid
	}
	if e := o.pre(now); e != oOK {
		return e
	}
	if !o.isHost(u) {
		if _, ok := o.members[u]; !ok {
			return oNotMember
		}
		return oForbidden
	}
	if o.phase != oRevealed {
		return oState
	}
	if (o.result.category != ResultDiverged && o.result.category != ResultNoValidVotes) || o.round >= o.roundLimit {
		return oState
	}
	o.votes = map[string]oVote{}
	o.round++
	o.startAt = now
	o.phase = oVoting
	o.result = nil
	o.accept(now)
	return oOK
}

type oPeek struct {
	phase      oPhase
	round      int
	votedCount int
	voterTotal int
	ownVote    *oVote
	outcome    *oResult
}

func (o *oracle) peek(u string, now int64) (*oPeek, oErr) {
	if e := o.pre(now); e != oOK {
		return nil, e
	}
	role, ok := o.members[u]
	if !ok {
		return nil, oNotMember
	}
	voted := o.votedCount()
	if o.phase != oVoting {
		voted = 0
		for vu := range o.revealedVoters {
			if r, ok := o.members[vu]; ok && r == RoleVoter {
				voted++
			}
		}
	}
	p := &oPeek{phase: o.phase, round: o.round, votedCount: voted, voterTotal: len(o.presentVoters())}
	if o.phase == oVoting {
		if role == RoleVoter {
			if v, ok := o.votes[u]; ok {
				vv := v
				p.ownVote = &vv
			}
		}
	} else {
		p.outcome = o.result
	}
	o.accept(now)
	return p, oOK
}
