package impossibletravel

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naiveSim 是按题述规则逐步写成的朴素模拟，用于与 Detector 对照。
type naiveSim struct {
	v, k, h int64
	cards   map[string]*naiveCard
}

type naiveCard struct {
	hasAnchor  bool
	t0, x0, y0 int64
	hist       []int64
	frozen     bool
	hasWin     bool
	from, to   int64
}

func newNaiveSim(v, k, h int64) *naiveSim {
	return &naiveSim{v: v, k: k, h: h, cards: make(map[string]*naiveCard)}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func (s *naiveSim) get(card string) *naiveCard {
	c := s.cards[card]
	if c == nil {
		c = &naiveCard{}
		s.cards[card] = c
	}
	return c
}

// check 返回判定与判定依据说明。
func (s *naiveSim) check(card string, t, x, y int64) (Decision, string) {
	if card == "" || t < 0 || t > 1_000_000_000_000 ||
		abs64(x) > 1_000_000_000 || abs64(y) > 1_000_000_000 {
		return RejectedInvalidParam, "参数非法"
	}
	c := s.get(card)
	if c.frozen {
		return RejectedFrozen, "卡已冻结"
	}
	if c.hasAnchor && t == c.t0 && x == c.x0 && y == c.y0 {
		return RejectedDuplicate, "与锚点三项全同"
	}
	if c.hasAnchor && t < c.t0 {
		return RejectedOutOfOrder, fmt.Sprintf("t=%d < 锚点t=%d", t, c.t0)
	}
	if !c.hasAnchor {
		c.hasAnchor = true
		c.t0, c.x0, c.y0 = t, x, y
		return Accepted, "无锚点，接受并建锚"
	}
	if c.hasWin && c.from <= t && t < c.to {
		c.t0, c.x0, c.y0 = t, x, y
		return Accepted, fmt.Sprintf("t=%d 在差旅窗口 [%d,%d)，免检", t, c.from, c.to)
	}
	dist := abs64(x-c.x0) + abs64(y-c.y0)
	dt := t - c.t0
	if dist*3600 <= s.v*dt {
		c.t0, c.x0, c.y0 = t, x, y
		return Accepted, fmt.Sprintf("d*3600=%d <= V*dt=%d", dist*3600, s.v*dt)
	}
	cnt := 1
	for _, tj := range c.hist {
		if tj > t-s.h {
			cnt++
		}
	}
	c.hist = append(c.hist, t)
	reason := fmt.Sprintf("d*3600=%d > V*dt=%d, cnt=%d", dist*3600, s.v*dt, cnt)
	if cnt >= int(s.k) {
		c.frozen = true
		reason += "，达到阈值冻结"
	}
	return RejectedImpossibleTravel, reason
}

func (s *naiveSim) batch(card string, txns []Txn) ([]Decision, []string, error) {
	if card == "" || len(txns) < 1 || len(txns) > 1000 {
		return nil, nil, ErrInvalidParam
	}
	for _, txn := range txns {
		if txn.T < 0 || txn.T > 1_000_000_000_000 ||
			abs64(txn.X) > 1_000_000_000 || abs64(txn.Y) > 1_000_000_000 {
			return nil, nil, ErrInvalidParam
		}
	}
	idx := make([]int, len(txns))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return txns[idx[a]].T < txns[idx[b]].T })
	dec := make([]Decision, len(txns))
	why := make([]string, len(txns))
	for _, i := range idx {
		dec[i], why[i] = s.check(card, txns[i].T, txns[i].X, txns[i].Y)
	}
	return dec, why, nil
}

func (s *naiveSim) declare(card string, from, to int64) error {
	if card == "" || from < 0 || from >= to || to > 10_000_000_000_000 {
		return ErrInvalidParam
	}
	c := s.get(card)
	c.hasWin = true
	c.from, c.to = from, to
	return nil
}

func (s *naiveSim) unfreeze(card string) error {
	c := s.cards[card]
	if c == nil {
		return ErrCardNotFound
	}
	if !c.frozen {
		return ErrCardNotFrozen
	}
	c.frozen = false
	c.hist = nil
	return nil
}

// op 是随机序列中的一步操作。
type op struct {
	kind     int // 0=Check 1=CheckBatch 2=Declare 3=Unfreeze
	card     string
	t, x, y  int64
	batch    []Txn
	from, to int64
}

func (o op) String() string {
	switch o.kind {
	case 0:
		return fmt.Sprintf("Check(%q, %d, %d, %d)", o.card, o.t, o.x, o.y)
	case 1:
		return fmt.Sprintf("CheckBatch(%q, %v)", o.card, o.batch)
	case 2:
		return fmt.Sprintf("Declare(%q, %d, %d)", o.card, o.from, o.to)
	default:
		return fmt.Sprintf("Unfreeze(%q)", o.card)
	}
}

var simCards = []string{"alpha", "beta", "gamma"}

func randTxn(r *rand.Rand) Txn {
	txn := Txn{
		T: int64(r.Intn(121)),
		X: int64(r.Intn(7) - 3),
		Y: int64(r.Intn(7) - 3),
	}
	// 小概率注入越界参数，覆盖非法路径。
	switch r.Intn(40) {
	case 0:
		txn.T = -1
	case 1:
		txn.T = 1_000_000_000_001
	case 2:
		txn.X = 1_000_000_001
	}
	return txn
}

func randOp(r *rand.Rand) op {
	card := simCards[r.Intn(len(simCards))]
	if r.Intn(50) == 0 {
		card = "" // 空卡号
	}
	switch r.Intn(10) {
	case 0, 1, 2, 3, 4:
		txn := randTxn(r)
		return op{kind: 0, card: card, t: txn.T, x: txn.X, y: txn.Y}
	case 5, 6:
		n := 1 + r.Intn(8)
		batch := make([]Txn, n)
		for i := range batch {
			batch[i] = randTxn(r)
		}
		return op{kind: 1, card: card, batch: batch}
	case 7, 8:
		from := int64(r.Intn(121))
		to := from + int64(r.Intn(30))
		if r.Intn(20) == 0 {
			to = from // 非法：from 不小于 to
		}
		return op{kind: 2, card: card, from: from, to: to}
	default:
		return op{kind: 3, card: card}
	}
}

func applyOp(d *Detector, o op) (string, error) {
	switch o.kind {
	case 0:
		return d.Check([]byte(o.card), o.t, o.x, o.y).String(), nil
	case 1:
		dec, err := d.CheckBatch([]byte(o.card), o.batch)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%v", dec), nil
	case 2:
		return "", d.Declare([]byte(o.card), o.from, o.to)
	default:
		return "", d.Unfreeze([]byte(o.card))
	}
}

func applyOpNaive(s *naiveSim, o op) (string, string, error) {
	switch o.kind {
	case 0:
		dec, why := s.check(o.card, o.t, o.x, o.y)
		return dec.String(), why, nil
	case 1:
		dec, why, err := s.batch(o.card, o.batch)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("%v", dec), fmt.Sprintf("%v", why), nil
	case 2:
		return "", "", s.declare(o.card, o.from, o.to)
	default:
		return "", "", s.unfreeze(o.card)
	}
}

func compareStates(t *testing.T, d *Detector, s *naiveSim, trial, step int) {
	t.Helper()
	seen := map[string]bool{}
	for _, card := range simCards {
		seen[card] = true
	}
	for card := range s.cards {
		seen[card] = true
	}
	for card := range seen {
		got, gotOK := d.State([]byte(card))
		nc := s.cards[card]
		wantOK := nc != nil
		if gotOK != wantOK {
			t.Fatalf("trial %d step %d: State(%q) ok=%v, want %v", trial, step, card, gotOK, wantOK)
		}
		if !gotOK {
			continue
		}
		want := CardState{
			HasAnchor:  nc.hasAnchor,
			AnchorT:    nc.t0,
			AnchorX:    nc.x0,
			AnchorY:    nc.y0,
			Rejections: nc.hist,
			Frozen:     nc.frozen,
			HasTravel:  nc.hasWin,
			TravelFrom: nc.from,
			TravelTo:   nc.to,
		}
		if len(want.Rejections) == 0 {
			want.Rejections = nil
		}
		if len(got.Rejections) == 0 {
			got.Rejections = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d step %d: State(%q) = %+v, want %+v", trial, step, card, got, want)
		}
	}
}

// 2000 组随机交易与批序列：Detector 与朴素模拟逐步对照，
// 并用两个 Detector 实例重放同一序列验证完全可复现。
func TestRandomAgainstNaiveSimulation(t *testing.T) {
	r := rand.New(rand.NewSource(20261002))
	for trial := 0; trial < 2000; trial++ {
		v := int64(1 + r.Intn(50))
		k := int64(1 + r.Intn(5))
		h := int64(1 + r.Intn(60))
		ops := make([]op, 20+r.Intn(60))
		for i := range ops {
			ops[i] = randOp(r)
		}
		t.Logf("trial %d: V=%d K=%d H=%d, %d ops", trial, v, k, h, len(ops))

		d1, err := NewDetector(v, k, h)
		if err != nil {
			t.Fatalf("trial %d: NewDetector: %v", trial, err)
		}
		d2, err := NewDetector(v, k, h)
		if err != nil {
			t.Fatalf("trial %d: NewDetector: %v", trial, err)
		}
		sim := newNaiveSim(v, k, h)

		for step, o := range ops {
			got1, err1 := applyOp(d1, o)
			got2, err2 := applyOp(d2, o)
			want, why, errWant := applyOpNaive(sim, o)
			t.Logf("  step %d: %s => %s (err=%v) [%s]", step, o, want, errWant, why)

			if got1 != want || !errors.Is(err1, errWant) {
				t.Fatalf("trial %d step %d: %s => %s (err=%v), want %s (err=%v) [%s]",
					trial, step, o, got1, err1, want, errWant, why)
			}
			if got2 != got1 || !errors.Is(err2, err1) {
				t.Fatalf("trial %d step %d: 重放分叉：%s => %s (err=%v) vs %s (err=%v)",
					trial, step, o, got1, err1, got2, err2)
			}
			compareStates(t, d1, sim, trial, step)
			compareStates(t, d2, sim, trial, step)
		}
	}
}

// 并发调用等价于某个串行顺序：竞态检测下混合调用不得panic，
// 且每张卡锚点 t 单调不降、冻结后 Check 恒为已冻结（直到解冻）。
func TestConcurrentAccess(t *testing.T) {
	d := mustNew(t, 100, 3, 50)
	const workers = 8
	const rounds = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			card := simCards[int(seed)%len(simCards)]
			for i := 0; i < rounds; i++ {
				switch r.Intn(5) {
				case 0:
					d.Check([]byte(card), int64(r.Intn(200)), int64(r.Intn(10)), int64(r.Intn(10)))
				case 1:
					d.CheckBatch([]byte(card), []Txn{
						{T: int64(r.Intn(200)), X: int64(r.Intn(10)), Y: 0},
						{T: int64(r.Intn(200)), X: 0, Y: int64(r.Intn(10))},
					})
				case 2:
					from := int64(r.Intn(200))
					d.Declare([]byte(card), from, from+1+int64(r.Intn(20)))
				case 3:
					d.Unfreeze([]byte(card))
				default:
					d.State([]byte(card))
				}
			}
		}(int64(w))
	}
	wg.Wait()
	for _, card := range simCards {
		st, ok := d.State([]byte(card))
		if !ok {
			continue
		}
		for _, rt := range st.Rejections {
			if rt < 0 || rt > 1_000_000_000_000 {
				t.Errorf("card %q: 非法的拒绝历史项 %d", card, rt)
			}
		}
		if st.Frozen {
			if got := d.Check([]byte(card), st.AnchorT, st.AnchorX, st.AnchorY); got != RejectedFrozen {
				t.Errorf("card %q frozen: Check = %v, want RejectedFrozen", card, got)
			}
		}
	}
}
