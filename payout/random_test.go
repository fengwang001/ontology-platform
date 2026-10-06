package payout_test

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"ontology/hold"
	"ontology/payout"
	"ontology/revenue"
)

// ---------- 朴素模拟：每次全量遍历全部份额，作为差分对照 ----------

type naiveShare struct {
	creator   string
	share     int64
	remaining int64
}

type naiveEvent struct {
	id       string
	content  string
	amount   int64
	time     int64
	shares   []*naiveShare
	refunded bool
}

type naiveHold struct {
	content  string
	from, to int64
}

type naive struct {
	wd, min  int64
	splits   map[string][]revenue.Part
	events   map[string]*naiveEvent
	order    []*naiveEvent
	creators map[string]bool
	holds    map[string]naiveHold
	debt     map[string]int64
	maxNow   int64
	hasNow   bool
	// 不变量统计（按创作者）
	earned    map[string]int64 // 累计入账份额
	refunded  map[string]int64 // 累计被退款份额（share 全额）
	newDebt   map[string]int64 // 累计新增欠款
	paidOut   map[string]int64 // 累计出款
	offsetSum map[string]int64 // 累计抵债
}

func newNaive(wd, min int64) *naive {
	return &naive{
		wd: wd, min: min,
		splits:    make(map[string][]revenue.Part),
		events:    make(map[string]*naiveEvent),
		creators:  make(map[string]bool),
		holds:     make(map[string]naiveHold),
		debt:      make(map[string]int64),
		earned:    make(map[string]int64),
		refunded:  make(map[string]int64),
		newDebt:   make(map[string]int64),
		paidOut:   make(map[string]int64),
		offsetSum: make(map[string]int64),
	}
}

func (n *naive) checkClock(now int64) error {
	if n.hasNow && now < n.maxNow {
		return revenue.ErrClockRewind
	}
	return nil
}

func (n *naive) accept(now int64) {
	if !n.hasNow || now > n.maxNow {
		n.maxNow = now
		n.hasNow = true
	}
}

func validNaiveNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (n *naive) setSplit(now int64, content string, parts []revenue.Part) error {
	if !validNaiveNow(now) || content == "" {
		return revenue.ErrInvalidParam
	}
	if len(parts) < 1 || len(parts) > 8 {
		return revenue.ErrInvalidParam
	}
	seen := make(map[string]bool, len(parts))
	sum := 0
	for _, p := range parts {
		if p.Creator == "" || p.BPS < 1 || p.BPS > 10000 || seen[p.Creator] {
			return revenue.ErrInvalidParam
		}
		seen[p.Creator] = true
		sum += p.BPS
	}
	if sum != 10000 {
		return revenue.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.splits[content] = append([]revenue.Part(nil), parts...)
	for _, p := range parts {
		n.creators[p.Creator] = true
	}
	n.accept(now)
	return nil
}

func (n *naive) earn(now int64, eventID, content string, amount int64) ([]int64, error) {
	if !validNaiveNow(now) || eventID == "" || content == "" || amount < 1 || amount > 1_000_000_000_000 {
		return nil, revenue.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	parts, ok := n.splits[content]
	if !ok {
		return nil, revenue.ErrNoSplit
	}
	if ev, ok := n.events[eventID]; ok {
		if ev.content != content || ev.amount != amount {
			return nil, revenue.ErrEventConflict
		}
		amounts := make([]int64, len(ev.shares))
		for i, s := range ev.shares {
			amounts[i] = s.share
		}
		n.accept(now)
		return amounts, nil
	}
	amounts := make([]int64, len(parts))
	var assigned int64
	for i, p := range parts {
		amounts[i] = amount * int64(p.BPS) / 10000
		assigned += amounts[i]
	}
	amounts[0] += amount - assigned
	ev := &naiveEvent{id: eventID, content: content, amount: amount, time: now}
	ev.shares = make([]*naiveShare, len(parts))
	for i, p := range parts {
		ev.shares[i] = &naiveShare{creator: p.Creator, share: amounts[i], remaining: amounts[i]}
		n.earned[p.Creator] += amounts[i]
	}
	n.events[eventID] = ev
	n.order = append(n.order, ev)
	n.accept(now)
	return amounts, nil
}

func (n *naive) hold(now int64, holdID, content string, from, to int64) error {
	if holdID == "" || content == "" || from < 0 || from >= to || !validNaiveNow(now) {
		return revenue.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.splits[content]; !ok {
		return revenue.ErrNoSplit
	}
	if _, ok := n.holds[holdID]; ok {
		return hold.ErrHoldExists
	}
	n.holds[holdID] = naiveHold{content: content, from: from, to: to}
	n.accept(now)
	return nil
}

func (n *naive) release(now int64, holdID string) error {
	if holdID == "" || !validNaiveNow(now) {
		return revenue.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.holds[holdID]; !ok {
		return hold.ErrHoldNotFound
	}
	delete(n.holds, holdID)
	n.accept(now)
	return nil
}

func (n *naive) held(content string, eventTime int64) bool {
	for _, h := range n.holds {
		if h.content == content && h.from <= eventTime && eventTime < h.to {
			return true
		}
	}
	return false
}

func (n *naive) balance(creator string, now int64) payout.Balance {
	var bal payout.Balance
	for _, ev := range n.order {
		for _, s := range ev.shares {
			if s.creator != creator || s.remaining == 0 {
				continue
			}
			switch {
			case n.held(ev.content, ev.time):
				bal.Held += s.remaining
			case now-ev.time < n.wd:
				bal.Pending += s.remaining
			default:
				bal.Available += s.remaining
			}
		}
	}
	bal.Debt = n.debt[creator]
	return bal
}

func (n *naive) settle(now int64, creator string) (int64, int64, error) {
	if creator == "" || !validNaiveNow(now) {
		return 0, 0, revenue.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return 0, 0, err
	}
	if !n.creators[creator] {
		return 0, 0, payout.ErrCreatorNotFound
	}
	var avail int64
	var availShares []*naiveShare
	for _, ev := range n.order {
		for _, s := range ev.shares {
			if s.creator != creator || s.remaining == 0 {
				continue
			}
			if n.held(ev.content, ev.time) || now-ev.time < n.wd {
				continue
			}
			avail += s.remaining
			availShares = append(availShares, s)
		}
	}
	debt := n.debt[creator]
	var paidOut, offset, consume int64
	switch {
	case avail <= debt:
		consume = avail
		offset = avail
	default:
		offset = debt
		if net := avail - debt; net >= n.min {
			consume = avail
			paidOut = net
		} else {
			consume = debt
		}
	}
	n.debt[creator] -= offset
	left := consume
	for _, s := range availShares {
		if left == 0 {
			break
		}
		take := min(s.remaining, left)
		s.remaining -= take
		left -= take
	}
	n.paidOut[creator] += paidOut
	n.offsetSum[creator] += offset
	n.accept(now)
	return paidOut, offset, nil
}

func (n *naive) refund(now int64, eventID string) error {
	if eventID == "" || !validNaiveNow(now) {
		return revenue.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	ev, ok := n.events[eventID]
	if !ok {
		return payout.ErrEventNotFound
	}
	if ev.refunded {
		return payout.ErrAlreadyRefunded
	}
	for _, s := range ev.shares {
		paid := s.share - s.remaining
		n.debt[s.creator] += paid
		n.newDebt[s.creator] += paid
		n.refunded[s.creator] += s.share
		s.remaining = 0
	}
	ev.refunded = true
	n.accept(now)
	return nil
}

// ---------- 随机操作序列生成 ----------

type opKind int

const (
	opSplit opKind = iota
	opEarn
	opHold
	opRelease
	opRefund
	opSettle
)

type op struct {
	kind    opKind
	now     int64
	id      string
	content string
	creator string
	amount  int64
	from    int64
	to      int64
	parts   []revenue.Part
}

func (o op) String() string {
	switch o.kind {
	case opSplit:
		return fmt.Sprintf("SetSplit(now=%d content=%s parts=%v)", o.now, o.content, o.parts)
	case opEarn:
		return fmt.Sprintf("Earn(now=%d id=%s content=%s amount=%d)", o.now, o.id, o.content, o.amount)
	case opHold:
		return fmt.Sprintf("Hold(now=%d id=%s content=%s from=%d to=%d)", o.now, o.id, o.content, o.from, o.to)
	case opRelease:
		return fmt.Sprintf("Release(now=%d id=%s)", o.now, o.id)
	case opRefund:
		return fmt.Sprintf("Refund(now=%d id=%s)", o.now, o.id)
	case opSettle:
		return fmt.Sprintf("Settle(now=%d creator=%s)", o.now, o.creator)
	}
	return "?"
}

var (
	genContents = []string{"c0", "c1", "c2"}
	genCreators = []string{"u0", "u1", "u2", "u3", "u4", "u5"}
)

func genParts(rnd *rand.Rand) []revenue.Part {
	k := 1 + rnd.Intn(3)
	perm := rnd.Perm(len(genCreators))[:k]
	parts := make([]revenue.Part, k)
	rem := 10000
	for i := 0; i < k-1; i++ {
		v := 1 + rnd.Intn(rem-(k-1-i))
		parts[i] = revenue.Part{Creator: genCreators[perm[i]], BPS: v}
		rem -= v
	}
	parts[k-1] = revenue.Part{Creator: genCreators[perm[k-1]], BPS: rem}
	// 10% 概率制造非法分成表
	if rnd.Float64() < 0.1 {
		if k > 1 && rnd.Float64() < 0.5 {
			parts[1].Creator = parts[0].Creator // 创作者重复
		} else {
			parts[0].BPS++ // 合计不为 10000
		}
	}
	return parts
}

func genOps(rnd *rand.Rand, count int) []op {
	ops := make([]op, 0, count)
	var maxNow int64
	type earnKey struct{ content, id string }
	earns := make(map[string]struct {
		content string
		amount  int64
	})
	var eventIDs, holdIDs []string
	nextEvent, nextHold := 0, 0
	pickNow := func() int64 {
		var now int64
		switch r := rnd.Float64(); {
		case r < 0.70:
			now = maxNow + rnd.Int63n(40)
		case r < 0.95:
			now = maxNow
		default:
			now = maxNow - rnd.Int63n(60)
			if now < 0 {
				now = 0
			}
		}
		if now > maxNow {
			maxNow = now
		}
		return now
	}
	for i := 0; i < count; i++ {
		switch w := rnd.Float64(); {
		case w < 0.15:
			ops = append(ops, op{kind: opSplit, now: pickNow(),
				content: genContents[rnd.Intn(len(genContents))], parts: genParts(rnd)})
		case w < 0.50:
			content := genContents[rnd.Intn(len(genContents))]
			if rnd.Float64() < 0.1 {
				content = "nosplit"
			}
			amount := int64(1 + rnd.Intn(2000))
			var id string
			switch r := rnd.Float64(); {
			case r < 0.10 && len(eventIDs) > 0: // 幂等重放：同 content 同 amount
				id = eventIDs[rnd.Intn(len(eventIDs))]
				rec := earns[id]
				content, amount = rec.content, rec.amount
			case r < 0.20 && len(eventIDs) > 0: // 事件冲突：同 id 不同 amount
				id = eventIDs[rnd.Intn(len(eventIDs))]
				content = earns[id].content
				amount = earns[id].amount + 1
			default:
				id = fmt.Sprintf("e%d", nextEvent)
				nextEvent++
			}
			if _, ok := earns[id]; !ok {
				earns[id] = struct {
					content string
					amount  int64
				}{content, amount}
				eventIDs = append(eventIDs, id)
			}
			ops = append(ops, op{kind: opEarn, now: pickNow(), id: id, content: content, amount: amount})
		case w < 0.62:
			from := rnd.Int63n(maxNow + 1)
			id := fmt.Sprintf("h%d", nextHold)
			nextHold++
			if rnd.Float64() < 0.2 && len(holdIDs) > 0 {
				id = holdIDs[rnd.Intn(len(holdIDs))] // 冻结已存在
			} else {
				holdIDs = append(holdIDs, id)
			}
			ops = append(ops, op{kind: opHold, now: pickNow(), id: id,
				content: genContents[rnd.Intn(len(genContents))], from: from, to: from + 1 + rnd.Int63n(200)})
		case w < 0.70:
			id := fmt.Sprintf("hx%d", rnd.Intn(1000))
			if rnd.Float64() < 0.7 && len(holdIDs) > 0 {
				id = holdIDs[rnd.Intn(len(holdIDs))]
			}
			ops = append(ops, op{kind: opRelease, now: pickNow(), id: id})
		case w < 0.80:
			id := fmt.Sprintf("ex%d", rnd.Intn(1000))
			if rnd.Float64() < 0.75 && len(eventIDs) > 0 {
				id = eventIDs[rnd.Intn(len(eventIDs))]
			}
			ops = append(ops, op{kind: opRefund, now: pickNow(), id: id})
		default:
			creator := genCreators[rnd.Intn(len(genCreators))]
			if rnd.Float64() < 0.15 {
				creator = "nobody"
			}
			ops = append(ops, op{kind: opSettle, now: pickNow(), creator: creator})
		}
	}
	return ops
}

// ---------- 差分驱动：真实账本 vs 朴素模拟 ----------

type opResult struct {
	err    error
	shares []int64
	paid   int64
	offset int64
}

func applyToLedger(l *payout.Ledger, o op) opResult {
	switch o.kind {
	case opSplit:
		return opResult{err: l.SetSplit(o.now, o.content, o.parts)}
	case opEarn:
		shares, err := l.Earn(o.now, o.id, o.content, o.amount)
		return opResult{err: err, shares: shares}
	case opHold:
		return opResult{err: l.Hold(o.now, o.id, o.content, o.from, o.to)}
	case opRelease:
		return opResult{err: l.Release(o.now, o.id)}
	case opRefund:
		return opResult{err: l.Refund(o.now, o.id)}
	case opSettle:
		paid, offset, err := l.Settle(o.now, o.creator)
		return opResult{err: err, paid: paid, offset: offset}
	}
	panic("bad op")
}

func applyToNaive(n *naive, o op) opResult {
	switch o.kind {
	case opSplit:
		return opResult{err: n.setSplit(o.now, o.content, o.parts)}
	case opEarn:
		shares, err := n.earn(o.now, o.id, o.content, o.amount)
		return opResult{err: err, shares: shares}
	case opHold:
		return opResult{err: n.hold(o.now, o.id, o.content, o.from, o.to)}
	case opRelease:
		return opResult{err: n.release(o.now, o.id)}
	case opRefund:
		return opResult{err: n.refund(o.now, o.id)}
	case opSettle:
		paid, offset, err := n.settle(o.now, o.creator)
		return opResult{err: err, paid: paid, offset: offset}
	}
	panic("bad op")
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a == b // 两侧均返回同一组哨兵错误
}

// TestRandomAgainstNaive 用 1500 组随机操作序列做差分对照：
// 每步核对输出一致与题述恒等式，日志打印输入、输出与判定依据；
// 每组序列再重放一次，验证出款序列一致。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	wds := []int64{0, 1, 5, 50, 200}
	mins := []int64{1, 3, 500, 1000}
	for seq := 0; seq < sequences; seq++ {
		rnd := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		wd := wds[rnd.Intn(len(wds))]
		minV := mins[rnd.Intn(len(mins))]
		ops := genOps(rnd, 60)

		l, err := payout.New(wd, minV)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		n := newNaive(wd, minV)
		var settles []opResult // 记录出款序列供重放对照

		for step, o := range ops {
			got := applyToLedger(l, o)
			want := applyToNaive(n, o)
			if !sameErr(got.err, want.err) {
				t.Fatalf("seq=%d step=%d\n输入: %s\n输出: err=%v\n判定: 朴素模拟 err=%v", seq, step, o, got.err, want.err)
			}
			if !slices.Equal(got.shares, want.shares) {
				t.Fatalf("seq=%d step=%d\n输入: %s\n输出: shares=%v\n判定: 朴素模拟 shares=%v", seq, step, o, got.shares, want.shares)
			}
			if got.paid != want.paid || got.offset != want.offset {
				t.Fatalf("seq=%d step=%d\n输入: %s\n输出: paid=%d offset=%d\n判定: 朴素模拟 paid=%d offset=%d",
					seq, step, o, got.paid, got.offset, want.paid, want.offset)
			}
			if o.kind == opEarn && got.err == nil {
				var sum int64
				for _, s := range got.shares {
					sum += s
				}
				if sum != o.amount {
					t.Fatalf("seq=%d step=%d\n输入: %s\n判定: 事件各份额之和 %d != amount %d", seq, step, o, sum, o.amount)
				}
			}
			if got.paid != 0 && got.paid < minV {
				t.Fatalf("seq=%d step=%d\n输入: %s\n输出: paid=%d\n判定: 出款非零则必须不小于 Min=%d", seq, step, o, got.paid, minV)
			}
			if o.kind == opSettle && got.err == nil {
				settles = append(settles, got)
			}
			// 每步对每位创作者核对 Balance 与恒等式
			now := n.maxNow
			for creator := range n.creators {
				bal := l.Balance(creator, now)
				nb := n.balance(creator, now)
				if bal != nb {
					t.Fatalf("seq=%d step=%d\n输入: %s\n输出: Balance(%s)=%+v\n判定: 朴素模拟 %+v",
						seq, step, o, creator, bal, nb)
				}
				lhs := n.earned[creator] - n.refunded[creator] + n.newDebt[creator]
				rhs := n.paidOut[creator] + n.offsetSum[creator] + bal.Pending + bal.Available + bal.Held
				if lhs != rhs {
					t.Fatalf("seq=%d step=%d\n输入: %s\n判定: 恒等式破坏 creator=%s 入账-退款+新增欠款=%d != 出款+抵债+pending+available+held=%d",
						seq, step, o, creator, lhs, rhs)
				}
				if bal.Debt != n.newDebt[creator]-n.offsetSum[creator] {
					t.Fatalf("seq=%d step=%d\n输入: %s\n判定: debt 恒等式破坏 creator=%s debt=%d != 新增欠款-抵债=%d",
						seq, step, o, creator, bal.Debt, n.newDebt[creator]-n.offsetSum[creator])
				}
			}
			t.Logf("seq=%d step=%d 输入=%s 输出={err=%v shares=%v paid=%d offset=%d} 判定=与朴素模拟一致且恒等式成立",
				seq, step, o, got.err, got.shares, got.paid, got.offset)
		}

		// 重放同一操作序列：出款序列必须一致
		replay, err := payout.New(wd, minV)
		if err != nil {
			t.Fatalf("seq=%d replay New: %v", seq, err)
		}
		idx := 0
		for _, o := range ops {
			got := applyToLedger(replay, o)
			if o.kind == opSettle && got.err == nil {
				if idx >= len(settles) || got.paid != settles[idx].paid || got.offset != settles[idx].offset {
					t.Fatalf("seq=%d 重放第 %d 次结算=(%d,%d), 首次=%v", seq, idx, got.paid, got.offset, settles[idx])
				}
				idx++
			}
		}
		if idx != len(settles) {
			t.Fatalf("seq=%d 重放结算次数=%d, 首次=%d", seq, idx, len(settles))
		}
	}
}
