package ontology_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	ontology "ontology"
	"ontology/hold"
	"ontology/payout"
	"ontology/revenue"
)

// naive is an independent, full-scan reference implementation used to
// cross-check the engine on random operation sequences.
type naive struct {
	wd, min  int64
	maxNow   int64
	splits   map[string][]revenue.Part
	creators map[string]bool
	events   map[string]*nEvent
	holds    map[string]nHold
	all      []*nShare
	debt     map[string]int64
}

type nShare struct {
	creator, content             string
	amount, remaining, eventTime int64
}

type nEvent struct {
	content  string
	amount   int64
	refunded bool
	shares   []*nShare
	infos    []revenue.Share
}

type nHold struct {
	content  string
	from, to int64
}

func newNaive(wd, min int64) *naive {
	return &naive{
		wd: wd, min: min, maxNow: -1,
		splits:   make(map[string][]revenue.Part),
		creators: make(map[string]bool),
		events:   make(map[string]*nEvent),
		holds:    make(map[string]nHold),
		debt:     make(map[string]int64),
	}
}

func (n *naive) clock(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return revenue.ErrInvalidParam
	}
	if now < n.maxNow {
		return revenue.ErrClockBack
	}
	return nil
}

func (n *naive) held(s *nShare) bool {
	for _, h := range n.holds {
		if h.content == s.content && s.eventTime >= h.from && s.eventTime < h.to {
			return true
		}
	}
	return false
}

func (n *naive) SetSplit(now int64, content string, parts []revenue.Part) error {
	if content == "" || len(parts) < 1 || len(parts) > 8 {
		return revenue.ErrInvalidParam
	}
	seen := map[string]bool{}
	var sum int64
	for _, p := range parts {
		if p.Creator == "" || p.Bps < 1 || p.Bps > 10000 || seen[p.Creator] {
			return revenue.ErrInvalidParam
		}
		seen[p.Creator] = true
		sum += p.Bps
	}
	if sum != 10000 {
		return revenue.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	n.splits[content] = append([]revenue.Part(nil), parts...)
	for _, p := range parts {
		n.creators[p.Creator] = true
	}
	n.maxNow = now
	return nil
}

func (n *naive) Earn(now int64, id, content string, amount int64) ([]revenue.Share, bool, error) {
	if id == "" || content == "" || amount < 1 || amount > 1_000_000_000_000 {
		return nil, false, revenue.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return nil, false, err
	}
	parts, ok := n.splits[content]
	if !ok {
		return nil, false, revenue.ErrNoSplit
	}
	if ev, ok := n.events[id]; ok {
		if ev.content == content && ev.amount == amount {
			return ev.infos, false, nil
		}
		return nil, false, revenue.ErrEventConflict
	}
	ev := &nEvent{content: content, amount: amount}
	var sum int64
	for _, p := range parts {
		a := amount * p.Bps / 10000
		sum += a
		ev.shares = append(ev.shares, &nShare{creator: p.Creator, content: content, amount: a, remaining: a, eventTime: now})
		ev.infos = append(ev.infos, revenue.Share{Creator: p.Creator, Amount: a})
	}
	if rem := amount - sum; rem > 0 {
		ev.shares[0].amount += rem
		ev.shares[0].remaining += rem
		ev.infos[0].Amount += rem
	}
	n.all = append(n.all, ev.shares...)
	n.events[id] = ev
	n.maxNow = now
	return ev.infos, true, nil
}

func (n *naive) Hold(now int64, id, content string, from, to int64) error {
	if id == "" || content == "" || from < 0 || from >= to || to > 1_000_000_000_000 {
		return hold.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if _, ok := n.splits[content]; !ok {
		return hold.ErrNoSplit
	}
	if _, ok := n.holds[id]; ok {
		return hold.ErrHoldExists
	}
	n.holds[id] = nHold{content: content, from: from, to: to}
	n.maxNow = now
	return nil
}

func (n *naive) Release(now int64, id string) error {
	if id == "" {
		return hold.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if _, ok := n.holds[id]; !ok {
		return hold.ErrHoldNotFound
	}
	delete(n.holds, id)
	n.maxNow = now
	return nil
}

func (n *naive) Balance(creator string, now int64) (held, pending, avail, debt int64) {
	for _, s := range n.all {
		if s.creator != creator || s.remaining == 0 {
			continue
		}
		switch {
		case n.held(s):
			held += s.remaining
		case now-s.eventTime >= n.wd:
			avail += s.remaining
		default:
			pending += s.remaining
		}
	}
	return held, pending, avail, n.debt[creator]
}

func (n *naive) Settle(now int64, creator string) (int64, int64, error) {
	if creator == "" {
		return 0, 0, payout.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return 0, 0, err
	}
	if !n.creators[creator] {
		return 0, 0, payout.ErrCreatorNotFound
	}
	var avail []*nShare
	var total int64
	for _, s := range n.all {
		if s.creator != creator || s.remaining == 0 || n.held(s) || now-s.eventTime < n.wd {
			continue
		}
		avail = append(avail, s)
		total += s.remaining
	}
	consume := func(amount int64) {
		for _, s := range avail {
			if amount == 0 {
				return
			}
			take := min(s.remaining, amount)
			s.remaining -= take
			amount -= take
		}
	}
	debt := n.debt[creator]
	var payoutAmt, offset int64
	if total <= debt {
		consume(total)
		n.debt[creator] = debt - total
		offset = total
	} else {
		consume(debt)
		n.debt[creator] = 0
		offset = debt
		if net := total - debt; net >= n.min {
			consume(net)
			payoutAmt = net
		}
	}
	n.maxNow = now
	return payoutAmt, offset, nil
}

// Refund returns the per-creator refunded share totals and new debt added.
func (n *naive) Refund(now int64, id string) (map[string][2]int64, error) {
	if id == "" {
		return nil, payout.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return nil, err
	}
	ev, ok := n.events[id]
	if !ok {
		return nil, payout.ErrEventNotFound
	}
	if ev.refunded {
		return nil, payout.ErrAlreadyRefunded
	}
	ev.refunded = true
	out := make(map[string][2]int64)
	for _, s := range ev.shares {
		paid := s.amount - s.remaining
		n.debt[s.creator] += paid
		s.remaining = 0
		v := out[s.creator]
		v[0] += s.amount
		v[1] += paid
		out[s.creator] = v
	}
	n.maxNow = now
	return out, nil
}

// ledger bundles the three facades of one engine instance.
type ledger struct {
	rev *revenue.Service
	hld *hold.Service
	pay *payout.Service
}

func mustNewLedger(t *testing.T, wd, min int64) ledger {
	t.Helper()
	r, h, p, err := ontology.New(wd, min)
	if err != nil {
		t.Fatal(err)
	}
	return ledger{r, h, p}
}

func sameErr(t *testing.T, label string, engineErr, naiveErr error) {
	t.Helper()
	if (engineErr == nil) != (naiveErr == nil) {
		t.Fatalf("%s: engine err=%v, naive err=%v", label, engineErr, naiveErr)
	}
	if engineErr != nil && !errors.Is(engineErr, naiveErr) {
		t.Fatalf("%s: engine err=%v does not match naive %v", label, engineErr, naiveErr)
	}
}

// TestRandomAgainstNaive replays 1500 random operation sequences on two
// independent engines and the full-scan naive model, compares every
// result, and checks the accounting identities after every step.
func TestRandomAgainstNaive(t *testing.T) {
	creatorPool := []string{"u0", "u1", "u2", "u3", "u4"}
	contentPool := []string{"c0", "c1", "c2"}
	wds := []int64{0, 1, 7, 100}
	mins := []int64{1, 50, 500}

	for seq := 0; seq < 1500; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 42))
		wd := wds[rng.Intn(len(wds))]
		minV := mins[rng.Intn(len(mins))]
		eng1 := mustNewLedger(t, wd, minV)
		eng2 := mustNewLedger(t, wd, minV)
		nv := newNaive(wd, minV)

		// per-creator cumulative counters for the identities
		earned := map[string]int64{}
		refundedShares := map[string]int64{}
		debtAdded := map[string]int64{}
		paidOut := map[string]int64{}
		offsets := map[string]int64{}

		type earnedEv struct {
			id      string
			content string
			amount  int64
		}
		var evs []earnedEv
		var holdIDs []string
		eventN, holdN := 0, 0
		now := int64(rng.Intn(10))
		steps := 40 + rng.Intn(40)

		checkInvariants := func(step int, opDesc string) {
			t.Helper()
			for _, c := range creatorPool {
				held, pending, avail, debt := eng1.pay.Balance(c, now)
				lhs := earned[c] - refundedShares[c] + debtAdded[c]
				rhs := paidOut[c] + offsets[c] + held + pending + avail
				ok := lhs == rhs && debt == debtAdded[c]-offsets[c]
				t.Logf("seq=%d step=%d %s creator=%s held=%d pending=%d avail=%d debt=%d | 恒等式 lhs=%d rhs=%d debt核对=%d-%d 判定=%v",
					seq, step, opDesc, c, held, pending, avail, debt, lhs, rhs, debtAdded[c], offsets[c], ok)
				if !ok {
					t.Fatalf("seq=%d step=%d %s creator=%s: identity broken lhs=%d rhs=%d debt=%d want %d",
						seq, step, opDesc, c, lhs, rhs, debt, debtAdded[c]-offsets[c])
				}
			}
		}

		for step := 0; step < steps; step++ {
			now += int64(rng.Intn(30))
			switch r := rng.Intn(100); {
			case r < 4: // 时钟回退或越界 now
				now -= int64(rng.Intn(60))
			}
			op := rng.Intn(100)
			switch {
			case op < 8: // SetSplit
				content := contentPool[rng.Intn(len(contentPool))]
				k := 1 + rng.Intn(3)
				perm := rng.Perm(len(creatorPool))[:k]
				var pts []revenue.Part
				sum := int64(0)
				for i, pi := range perm {
					bps := int64(1 + rng.Intn(5000))
					if i == len(perm)-1 {
						bps = 10000 - sum
					}
					if bps < 1 {
						bps = 1
					}
					sum += bps
					pts = append(pts, revenue.Part{Creator: creatorPool[pi], Bps: bps})
				}
				if sum != 10000 { // 修正最后一项使合计恰为 10000
					pts[len(pts)-1].Bps += 10000 - sum
				}
				if rng.Intn(10) == 0 { // 注入非法分成表
					pts = append(pts, pts[0])
				}
				err1 := eng1.rev.SetSplit(now, content, pts)
				err2 := eng2.rev.SetSplit(now, content, pts)
				errN := nv.SetSplit(now, content, pts)
				sameErr(t, "setsplit eng1", err1, errN)
				sameErr(t, "setsplit eng2", err2, errN)
				t.Logf("seq=%d step=%d setsplit now=%d content=%s parts=%v -> err=%v (判定: 与朴素模型一致)",
					seq, step, now, content, pts, errN)

			case op < 38: // Earn
				content := contentPool[rng.Intn(len(contentPool))]
				amount := int64(1 + rng.Intn(2000))
				var id string
				switch x := rng.Intn(100); {
				case x < 5:
					amount = 0 // 非法金额
					id = fmt.Sprintf("e%d", eventN)
					eventN++
				case x < 8:
					amount = 1_000_000_000_000 // 上限大金额
					id = fmt.Sprintf("e%d", eventN)
					eventN++
				case x < 23 && len(evs) > 0: // 幂等重放
					ev := evs[rng.Intn(len(evs))]
					id, content, amount = ev.id, ev.content, ev.amount
				case x < 33 && len(evs) > 0: // 冲突
					ev := evs[rng.Intn(len(evs))]
					id, content, amount = ev.id, ev.content, ev.amount+1
				default:
					id = fmt.Sprintf("e%d", eventN)
					eventN++
				}
				sh1, err1 := eng1.rev.Earn(now, id, content, amount)
				sh2, err2 := eng2.rev.Earn(now, id, content, amount)
				shN, created, errN := nv.Earn(now, id, content, amount)
				sameErr(t, "earn eng1", err1, errN)
				sameErr(t, "earn eng2", err2, errN)
				if errN == nil {
					if !reflect.DeepEqual(sh1, shN) || !reflect.DeepEqual(sh2, shN) {
						t.Fatalf("seq=%d step=%d earn: shares %v / %v vs naive %v", seq, step, sh1, sh2, shN)
					}
					var sum int64
					for _, s := range shN {
						sum += s.Amount
					}
					if sum != amount {
						t.Fatalf("seq=%d step=%d earn: share sum %d != amount %d", seq, step, sum, amount)
					}
					if created {
						evs = append(evs, earnedEv{id, content, amount})
						for _, s := range shN {
							earned[s.Creator] += s.Amount
						}
					}
				}
				t.Logf("seq=%d step=%d earn now=%d id=%s content=%s amount=%d -> shares=%v err=%v (判定: 双引擎与朴素一致, 份额和==amount)",
					seq, step, now, id, content, amount, shN, errN)

			case op < 48: // Hold
				content := contentPool[rng.Intn(len(contentPool))]
				id := fmt.Sprintf("h%d", holdN)
				holdN++
				if rng.Intn(10) == 0 && holdN > 1 {
					id = fmt.Sprintf("h%d", rng.Intn(holdN)) // 可能重复
				}
				from := now - int64(rng.Intn(60))
				to := from + 1 + int64(rng.Intn(120))
				if rng.Intn(20) == 0 {
					to = from // 非法区间
				}
				err1 := eng1.hld.Hold(now, id, content, from, to)
				err2 := eng2.hld.Hold(now, id, content, from, to)
				errN := nv.Hold(now, id, content, from, to)
				sameErr(t, "hold eng1", err1, errN)
				sameErr(t, "hold eng2", err2, errN)
				if errN == nil {
					holdIDs = append(holdIDs, id)
				}
				t.Logf("seq=%d step=%d hold now=%d id=%s content=%s [%d,%d) -> err=%v (判定: 与朴素一致)",
					seq, step, now, id, content, from, to, errN)

			case op < 56: // Release
				id := fmt.Sprintf("h%d", rng.Intn(max(holdN, 1)))
				err1 := eng1.hld.Release(now, id)
				err2 := eng2.hld.Release(now, id)
				errN := nv.Release(now, id)
				sameErr(t, "release eng1", err1, errN)
				sameErr(t, "release eng2", err2, errN)
				t.Logf("seq=%d step=%d release now=%d id=%s -> err=%v (判定: 与朴素一致)",
					seq, step, now, id, errN)

			case op < 66: // Refund
				id := fmt.Sprintf("e%d", rng.Intn(max(eventN, 1)))
				err1 := eng1.pay.Refund(now, id)
				err2 := eng2.pay.Refund(now, id)
				delta, errN := nv.Refund(now, id)
				sameErr(t, "refund eng1", err1, errN)
				sameErr(t, "refund eng2", err2, errN)
				if errN == nil {
					for c, v := range delta {
						refundedShares[c] += v[0]
						debtAdded[c] += v[1]
					}
				}
				t.Logf("seq=%d step=%d refund now=%d id=%s -> err=%v delta=%v (判定: 与朴素一致)",
					seq, step, now, id, errN, delta)

			case op < 83: // Settle
				creator := creatorPool[rng.Intn(len(creatorPool))]
				if rng.Intn(20) == 0 {
					creator = "ghost"
				}
				p1, o1, err1 := eng1.pay.Settle(now, creator)
				p2, o2, err2 := eng2.pay.Settle(now, creator)
				pN, oN, errN := nv.Settle(now, creator)
				sameErr(t, "settle eng1", err1, errN)
				sameErr(t, "settle eng2", err2, errN)
				if errN == nil {
					if p1 != pN || o1 != oN || p2 != pN || o2 != oN {
						t.Fatalf("seq=%d step=%d settle %s: eng=(%d,%d)/(%d,%d) naive=(%d,%d)",
							seq, step, creator, p1, o1, p2, o2, pN, oN)
					}
					if pN != 0 && pN < minV {
						t.Fatalf("seq=%d step=%d settle: payout %d < Min %d", seq, step, pN, minV)
					}
					paidOut[creator] += pN
					offsets[creator] += oN
				}
				t.Logf("seq=%d step=%d settle now=%d creator=%s -> payout=%d offset=%d err=%v (判定: 双引擎==朴素, 出款为0或>=Min)",
					seq, step, now, creator, pN, oN, errN)

			default: // Balance
				creator := creatorPool[rng.Intn(len(creatorPool))]
				h1, p1, a1, d1 := eng1.pay.Balance(creator, now)
				h2, p2, a2, d2 := eng2.pay.Balance(creator, now)
				hN, pN, aN, dN := nv.Balance(creator, now)
				if h1 != hN || p1 != pN || a1 != aN || d1 != dN || h2 != hN || p2 != pN || a2 != aN || d2 != dN {
					t.Fatalf("seq=%d step=%d balance %s: eng=(%d,%d,%d,%d)/(%d,%d,%d,%d) naive=(%d,%d,%d,%d)",
						seq, step, creator, h1, p1, a1, d1, h2, p2, a2, d2, hN, pN, aN, dN)
				}
				t.Logf("seq=%d step=%d balance now=%d creator=%s -> (%d,%d,%d,%d) (判定: 双引擎==朴素)",
					seq, step, now, creator, hN, pN, aN, dN)
			}
			checkInvariants(step, "post-op")
		}
		_ = holdIDs
	}
}
