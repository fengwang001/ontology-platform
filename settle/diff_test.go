package settle

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/fund"
)

// 朴素模型：完全按规格逐分累计的独立实现。

type mAcc struct {
	du, x, f, p, q int64
}

type mItem struct {
	class    string
	pct      int
	limit    int64
	price    int64
	quantity int64
	code     string
}

type mRecord struct {
	id, person string
	year       int
	reversed   bool
	before     mAcc
	a, e       int64
	f1, f2, f3 int64
	self       int64
	aidOn      bool
}

type model struct {
	d, s1, s2, capF, d2, capA int64
	r1, r2, r3, rd, ra        int

	cat    map[string]mItem
	person map[string]bool
	aid    map[string]bool
	acc    map[string]map[int]mAcc
	maxY   map[string]int
	ids    map[string]*mRecord
	open   map[string][]*mRecord
	clock  int64
}

func newModel(d, s1, s2 int64, r1, r2, r3 int, capF, d2 int64, rd, ra int, capA int64) *model {
	return &model{
		d: d, s1: s1, s2: s2, capF: capF, d2: d2, capA: capA,
		r1: r1, r2: r2, r3: r3, rd: rd, ra: ra,
		cat: map[string]mItem{}, person: map[string]bool{}, aid: map[string]bool{},
		acc: map[string]map[int]mAcc{}, maxY: map[string]int{},
		ids: map[string]*mRecord{}, open: map[string][]*mRecord{},
	}
}

type mOp struct {
	kind   string // settle / reverse / aid
	now    int64
	id     string
	person string
	year   int
	on     bool
	items  []mItem
}

type mErr string

const (
	mOK         mErr = ""
	mParam      mErr = "param"
	mClock      mErr = "clock"
	mPerson     mErr = "person"
	mDup        mErr = "dup"
	mCatalog    mErr = "catalog"
	mYearClosed mErr = "year"
	mNotFound   mErr = "notfound"
	mReversed   mErr = "reversed"
	mNotLast    mErr = "notlast"
)

func (m *model) accOf(person string, year int) mAcc {
	if ys, ok := m.acc[person]; ok {
		return ys[year]
	}
	return mAcc{}
}

func minI(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (m *model) settle(op mOp) (*mRecord, mErr) {
	if len(op.items) < 1 || len(op.items) > 200 || op.now < 0 || op.now > maxNow ||
		op.id == "" || op.person == "" || op.year < 1 || op.year > 9999 {
		return nil, mParam
	}
	for _, it := range op.items {
		if it.price < 1 || it.price > 1_000_000_000 || it.quantity < 1 || it.quantity > 10_000 {
			return nil, mParam
		}
	}
	if op.now < m.clock {
		return nil, mClock
	}
	if !m.person[op.person] {
		return nil, mPerson
	}
	if _, ok := m.ids[op.id]; ok {
		return nil, mDup
	}
	missing := -1
	for i, it := range op.items {
		if _, ok := m.cat[it.code]; !ok {
			missing = i
			break
		}
	}
	if missing >= 0 {
		return nil, mCatalog
	}
	if maxY := m.maxY[op.person]; op.year < maxY {
		return nil, mYearClosed
	}

	var A, E int64
	for _, it := range op.items {
		def := m.cat[it.code]
		a := it.price * it.quantity
		unit := it.price
		if def.limit > 0 && unit > def.limit {
			unit = def.limit
		}
		b := unit * it.quantity
		var c int64
		switch def.class {
		case "乙":
			c = (b*int64(def.pct) + 99) / 100
		case "丙":
			c = b
		}
		A += a
		E += b - c
	}

	before := m.accOf(op.person, op.year)
	g := minI(E, m.d-before.du)
	x := E - g
	lo, hi := before.x, before.x+x
	var m1, m2, m3 int64
	if lo < m.s1 {
		m1 = minI(hi, m.s1) - lo
	}
	if l2, r2 := maxI(lo, m.s1), minI(hi, m.s2); l2 < r2 {
		m2 = r2 - l2
	}
	if hi > m.s2 {
		m3 = hi - maxI(lo, m.s2)
	}
	f := (m1*int64(m.r1) + m2*int64(m.r2) + m3*int64(m.r3)) / 100
	f1 := minI(f, m.capF-before.f)
	pay := E - f1
	y := maxI(0, before.p+pay-m.d2) - maxI(0, before.p-m.d2)
	f2 := y * int64(m.rd) / 100
	z := E - f1 - f2
	var f3 int64
	aidOn := m.aid[op.person]
	if aidOn {
		f3 = minI(z*int64(m.ra)/100, m.capA-before.q)
	}
	self := A - f1 - f2 - f3

	after := mAcc{
		du: before.du + g,
		x:  before.x + x,
		f:  before.f + f1,
		p:  before.p + pay,
		q:  before.q + f3,
	}
	if _, ok := m.acc[op.person]; !ok {
		m.acc[op.person] = map[int]mAcc{}
	}
	m.acc[op.person][op.year] = after
	if op.year > m.maxY[op.person] {
		m.maxY[op.person] = op.year
	}
	rec := &mRecord{
		id: op.id, person: op.person, year: op.year, before: before,
		a: A, e: E, f1: f1, f2: f2, f3: f3, self: self, aidOn: aidOn,
	}
	m.ids[op.id] = rec
	m.open[op.person] = append(m.open[op.person], rec)
	m.clock = op.now
	return rec, mOK
}

func (m *model) reverse(op mOp) (*mRecord, mErr) {
	if op.now < 0 || op.now > maxNow || op.id == "" {
		return nil, mParam
	}
	if op.now < m.clock {
		return nil, mClock
	}
	rec, ok := m.ids[op.id]
	if !ok {
		return nil, mNotFound
	}
	if rec.reversed {
		return nil, mReversed
	}
	stack := m.open[rec.person]
	if stack[len(stack)-1].id != op.id {
		return nil, mNotLast
	}
	if _, ok := m.acc[rec.person]; !ok {
		m.acc[rec.person] = map[int]mAcc{}
	}
	m.acc[rec.person][rec.year] = rec.before
	rec.reversed = true
	m.open[rec.person] = stack[:len(stack)-1]
	maxY := 0
	for y := range m.acc[rec.person] {
		// 仅仍有未冲正结算的年度计入 maxYear。
		open := false
		for _, r := range m.open[rec.person] {
			if r.year == y {
				open = true
				break
			}
		}
		if open && y > maxY {
			maxY = y
		}
	}
	m.maxY[rec.person] = maxY
	m.clock = op.now
	return rec, mOK
}

func (m *model) setAid(now int64, person string, on bool) mErr {
	if now < 0 || now > maxNow || person == "" {
		return mParam
	}
	if now < m.clock {
		return mClock
	}
	if !m.person[person] {
		return mPerson
	}
	m.aid[person] = on
	m.clock = now
	return mOK
}

func engineErrClass(err error) mErr {
	switch {
	case err == nil:
		return mOK
	case errors.Is(err, ErrInvalidParam):
		return mParam
	case errors.Is(err, ErrClockRollback):
		return mClock
	case errors.Is(err, ErrPersonNotFound):
		return mPerson
	case errors.Is(err, ErrDuplicateSettlement):
		return mDup
	case errors.Is(err, ErrItemNotFound):
		return mCatalog
	case errors.Is(err, ErrYearClosed):
		return mYearClosed
	case errors.Is(err, ErrSettlementNotFound):
		return mNotFound
	case errors.Is(err, ErrAlreadyReversed):
		return mReversed
	case errors.Is(err, ErrNotLast):
		return mNotLast
	default:
		return mErr("unknown:" + err.Error())
	}
}

func mAccToFund(a mAcc) fund.Acc {
	return fund.Acc{Du: a.du, X: a.x, F: a.f, P: a.p, Q: a.q}
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	const maxOps = 60
	for seed := int64(1); seed <= sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		runOneSequence(t, seed, rng, maxOps)
	}
	t.Logf("差分完成：%d 组随机操作序列引擎与朴素模型逐步一致", sequences)
}

func runOneSequence(t *testing.T, seed int64, rng *rand.Rand, maxOps int) {
	t.Helper()
	logf := func(format string, args ...any) {
		t.Logf("[seed=%d] "+format, append([]any{seed}, args...)...)
	}

	d := int64(rng.Intn(2000))
	s1 := int64(rng.Intn(5000) + 1)
	s2 := s1 + int64(rng.Intn(5000)+1)
	capF := int64(rng.Intn(30000))
	d2 := int64(rng.Intn(10000))
	capA := int64(rng.Intn(5000))
	pct := func() int { return rng.Intn(101) }

	eng, err := New(d, s1, s2, pct(), pct(), pct(), capF, d2, pct(), pct(), capA)
	if err != nil {
		t.Fatalf("seed=%d New: %v", seed, err)
	}
	md := newModel(d, s1, s2, eng.r1, eng.r2, eng.r3, capF, d2, eng.rd, eng.ra, capA)

	const catalogSize = 12
	codes := make([]string, catalogSize)
	for i := 0; i < catalogSize; i++ {
		code := fmt.Sprintf("c%d", i)
		codes[i] = code
		class := []string{"甲", "乙", "丙"}[rng.Intn(3)]
		var p int
		if class == "乙" {
			p = 1 + rng.Intn(99)
		}
		limit := int64(rng.Intn(5001)) // 0 表示无限价
		if err := eng.AddItem(code, class, p, limit); err != nil {
			t.Fatalf("seed=%d AddItem: %v", seed, err)
		}
		md.cat[code] = mItem{class: class, pct: p, limit: limit, code: code}
	}

	const peopleN = 3
	names := make([]string, peopleN)
	for i := 0; i < peopleN; i++ {
		names[i] = fmt.Sprintf("p%d", i)
		if err := eng.AddPerson(names[i]); err != nil {
			t.Fatalf("AddPerson: %v", err)
		}
		md.person[names[i]] = true
	}

	type settled struct {
		id   string
		year int
	}
	history := make([][]settled, peopleN)
	clock := int64(0)
	nextNow := func() int64 {
		if rng.Intn(6) == 0 && clock > 0 {
			return clock - 1 // 故意回退
		}
		clock += int64(rng.Intn(3))
		return clock
	}

	for step := 0; step < maxOps; step++ {
		roll := rng.Intn(100)
		switch {
		case roll < 45: // Settle
			pi := rng.Intn(peopleN)
			person := names[pi]
			n := 1 + rng.Intn(3)
			eitems := make([]Item, n)
			mitems := make([]mItem, n)
			injectMissing := false
			for i := 0; i < n; i++ {
				code := codes[rng.Intn(catalogSize)]
				if i == n-1 && rng.Intn(8) == 0 {
					code = "missing"
					injectMissing = true
				}
				price := int64(1 + rng.Intn(8000))
				qty := int64(1 + rng.Intn(4))
				eitems[i] = Item{Code: code, Price: price, Quantity: qty}
				def := md.cat[code]
				mitems[i] = mItem{class: def.class, pct: def.pct, limit: def.limit,
					code: code, price: price, quantity: qty}
			}
			year := 1 + rng.Intn(4)
			id := fmt.Sprintf("p%d-step%d", pi, step)
			if rng.Intn(8) == 0 && len(history[pi]) > 0 {
				id = history[pi][rng.Intn(len(history[pi]))].id
			}
			now := nextNow()
			op := mOp{kind: "settle", now: now, id: id, person: person, year: year, items: mitems}
			logf("step=%d Settle now=%d id=%q year=%d n=%d missing=%v", step, now, id, year, n, injectMissing)

			res, eerr := eng.Settle(now, id, person, year, eitems)
			mrec, mclass := md.settle(op)
			eclass := engineErrClass(eerr)
			if eclass != mclass {
				logf("拒绝类别分歧 引擎=%v(%v) 模型=%v", eclass, eerr, mclass)
				t.Fatalf("seed=%d step=%d settle 判定分歧", seed, step)
			}
			if eclass == mOK {
				if clock < now {
					clock = now
				}
				history[pi] = append(history[pi], settled{id: id, year: year})
				if res.A != mrec.a || res.E != mrec.e || res.F1 != mrec.f1 ||
					res.F2 != mrec.f2 || res.F3 != mrec.f3 || res.Self != mrec.self ||
					res.AidOn != mrec.aidOn {
					logf("支付分歧 引擎 A=%d E=%d F1=%d F2=%d F3=%d self=%d aid=%v",
						res.A, res.E, res.F1, res.F2, res.F3, res.Self, res.AidOn)
					logf("        模型 A=%d E=%d F1=%d F2=%d F3=%d self=%d aid=%v",
						mrec.a, mrec.e, mrec.f1, mrec.f2, mrec.f3, mrec.self, mrec.aidOn)
					t.Fatalf("seed=%d step=%d settle 结果分歧", seed, step)
				}
				if res.A != res.F1+res.F2+res.F3+res.Self {
					t.Fatalf("seed=%d 恒等式破坏", seed)
				}
				if res.After != mAccToFund(md.accOf(person, year)) {
					t.Fatalf("seed=%d After 与模型不一致", seed)
				}
				logf("  => A=%d E=%d g=%d x=%d(m1=%d,m2=%d,m3=%d) F1=%d p=%d y=%d F2=%d z=%d F3=%d self=%d",
					res.A, res.E, res.G, res.X, res.M1, res.M2, res.M3,
					res.F1, res.P, res.Y, res.F2, res.Z, res.F3, res.Self)
			} else {
				logf("  => 拒绝 引擎=%v 模型=%v（判定一致）", eclass, mclass)
			}

		case roll < 80: // Reverse
			pi := rng.Intn(peopleN)
			id := "never"
			if len(history[pi]) > 0 {
				if rng.Intn(4) == 0 && len(history[pi]) > 1 {
					id = history[pi][0].id // 故意非栈顶
				} else {
					id = history[pi][len(history[pi])-1].id
				}
			}
			if rng.Intn(10) == 0 {
				id = "never"
			}
			now := nextNow()
			logf("step=%d Reverse now=%d id=%q", step, now, id)
			res, eerr := eng.Reverse(now, id)
			mrec, mclass := md.reverse(mOp{kind: "reverse", now: now, id: id})
			eclass := engineErrClass(eerr)
			if eclass != mclass {
				logf("拒绝类别分歧 引擎=%v(%v) 模型=%v", eclass, eerr, mclass)
				t.Fatalf("seed=%d step=%d reverse 判定分歧", seed, step)
			}
			if eclass == mOK {
				if clock < now {
					clock = now
				}
				history[pi] = history[pi][:len(history[pi])-1]
				want := mAccToFund(mrec.before)
				if res.After != want {
					t.Fatalf("seed=%d reverse 后累计器 %+v, 模型恢复 %+v", seed, res.After, want)
				}
				logf("  => 冲正成功 person=%s year=%d 恢复到 %+v", mrec.person, mrec.year, res.After)
			} else {
				logf("  => 拒绝 引擎=%v 模型=%v（判定一致）", eclass, mclass)
			}

		default: // SetAid
			pi := rng.Intn(peopleN)
			on := rng.Intn(2) == 0
			now := nextNow()
			logf("step=%d SetAid now=%d person=%q on=%v", step, now, names[pi], on)
			eerr := eng.SetAid(now, names[pi], on)
			mclass := md.setAid(now, names[pi], on)
			eclass := engineErrClass(eerr)
			if eclass != mclass {
				t.Fatalf("seed=%d step=%d aid 判定分歧 %v vs %v", seed, step, eclass, mclass)
			}
			if eclass == mOK && clock < now {
				clock = now
			}
			logf("  => %v", eclass)
		}

		// 每步后核对全员全年五元组、身份、时钟及不变量。
		for pi, person := range names {
			for year := 1; year <= 4; year++ {
				g, _ := eng.Acc(person, year)
				want := md.accOf(person, year)
				if g != mAccToFund(want) {
					t.Fatalf("seed=%d step=%d person=%s year=%d 引擎=%+v 模型=%+v",
						seed, step, person, year, g, want)
				}
				if g.Du > d || g.F > capF || g.Q > capA ||
					g.Du < 0 || g.X < 0 || g.F < 0 || g.P < 0 || g.Q < 0 {
					t.Fatalf("seed=%d 不变量破坏 %+v", seed, g)
				}
			}
			if eng.Aid(person) != md.aid[names[pi]] {
				t.Fatalf("seed=%d 身份分歧", seed)
			}
		}
		if eng.Clock() != md.clock {
			t.Fatalf("seed=%d step=%d 时钟 %d vs %d", seed, step, eng.Clock(), md.clock)
		}
	}

	// 收尾：把每人未冲正笔全部按 LIFO 冲正，五元组必须归零。
	for _, person := range names {
		for len(md.open[person]) > 0 {
			top := md.open[person][len(md.open[person])-1]
			clock++
			if _, err := eng.Reverse(clock, top.id); err != nil {
				t.Fatalf("seed=%d 收尾冲正: %v", seed, err)
			}
			if _, merr := md.reverse(mOp{kind: "reverse", now: clock, id: top.id}); merr != mOK {
				t.Fatalf("seed=%d 模型收尾: %v", seed, merr)
			}
		}
		for year := 1; year <= 4; year++ {
			g, _ := eng.Acc(person, year)
			if g != (fund.Acc{}) {
				t.Fatalf("seed=%d 全部冲正后 person=%s year=%d 非零 %+v", seed, person, year, g)
			}
		}
	}
}
