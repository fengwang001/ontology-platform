package sunset

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naiveEngine 是逐消费者全量扫描的朴素参照实现，
// 与 Engine 独立编写，用于随机操作序列的差分对照。
type naiveEngine struct {
	nmin, bw, pd, x, q, xmax int64
	maxNow                   int64
	hasNow                   bool
	phase                    map[string]int // 0=Active 1=Deprecated 2=Brownout 3=Retired
	children                 map[string][]string
	sunsetAt                 map[string]int64
	brownStart               map[string]int64
	extCount                 map[string]int
	extTotal                 map[string]int64
	lastAccess               map[string]map[string]int64
	acked                    map[string]map[string]bool
}

func newNaive(nmin, bw, pd, x, q, xmax int64) *naiveEngine {
	return &naiveEngine{
		nmin: nmin, bw: bw, pd: pd, x: x, q: q, xmax: xmax,
		phase:      make(map[string]int),
		children:   make(map[string][]string),
		sunsetAt:   make(map[string]int64),
		brownStart: make(map[string]int64),
		extCount:   make(map[string]int),
		extTotal:   make(map[string]int64),
		lastAccess: make(map[string]map[string]int64),
		acked:      make(map[string]map[string]bool),
	}
}

func (n *naiveEngine) clockOK(now int64) error {
	if now < 0 || now > maxTimestamp {
		return ErrInvalidParam
	}
	if n.hasNow && now < n.maxNow {
		return ErrClockRegression
	}
	return nil
}

func (n *naiveEngine) commit(now int64) {
	if !n.hasNow || now > n.maxNow {
		n.maxNow, n.hasNow = now, true
	}
}

func (n *naiveEngine) exists(d string) bool {
	_, ok := n.phase[d]
	return ok
}

func (n *naiveEngine) addDataset(name string, parents []string, now int64) error {
	if name == "" || len(parents) > 8 {
		return ErrInvalidParam
	}
	seen := map[string]bool{}
	for _, p := range parents {
		if p == "" || seen[p] {
			return ErrInvalidParam
		}
		seen[p] = true
	}
	if err := n.clockOK(now); err != nil {
		return err
	}
	if n.exists(name) {
		return ErrDatasetExists
	}
	for _, p := range parents {
		if !n.exists(p) {
			return ErrDatasetNotFound
		}
	}
	n.phase[name] = 0
	for _, p := range parents {
		n.children[p] = append(n.children[p], name)
	}
	n.commit(now)
	return nil
}

func (n *naiveEngine) impact(d string) []string {
	seen := map[string]bool{}
	stack := append([]string(nil), n.children[d]...)
	out := []string{}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[c] {
			continue
		}
		seen[c] = true
		if n.phase[c] != 3 {
			out = append(out, c)
		}
		stack = append(stack, n.children[c]...)
	}
	sort.Strings(out)
	return out
}

func (n *naiveEngine) deprecate(d string, notice, now int64) ([]string, error) {
	if d == "" {
		return nil, ErrInvalidParam
	}
	if err := n.clockOK(now); err != nil {
		return nil, err
	}
	if !n.exists(d) {
		return nil, ErrDatasetNotFound
	}
	if n.phase[d] != 0 {
		return nil, ErrPhase
	}
	if notice < n.nmin {
		return nil, ErrNoticeTooShort
	}
	n.phase[d] = 1
	n.sunsetAt[d] = now + notice
	n.brownStart[d] = n.sunsetAt[d] - n.bw
	n.commit(now)
	return n.impact(d), nil
}

func (n *naiveEngine) undeprecate(d string, now int64) error {
	if d == "" {
		return ErrInvalidParam
	}
	if err := n.clockOK(now); err != nil {
		return err
	}
	if !n.exists(d) {
		return ErrDatasetNotFound
	}
	if n.phase[d] != 1 {
		return ErrPhase
	}
	n.phase[d] = 0
	n.commit(now)
	return nil
}

func (n *naiveEngine) recordAccess(d, c string, now int64) {
	if n.lastAccess[d] == nil {
		n.lastAccess[d] = map[string]int64{}
		n.acked[d] = map[string]bool{}
	}
	n.lastAccess[d][c] = now
	n.acked[d][c] = false
}

func (n *naiveEngine) access(c, d string, now int64) (bool, error) {
	if c == "" || d == "" {
		return false, ErrInvalidParam
	}
	if err := n.clockOK(now); err != nil {
		return false, err
	}
	if !n.exists(d) {
		return false, ErrDatasetNotFound
	}
	switch n.phase[d] {
	case 0:
		n.recordAccess(d, c, now)
		n.commit(now)
		return false, nil
	case 1:
		n.recordAccess(d, c, now)
		n.commit(now)
		return true, nil
	case 2:
		elapsed := now - n.brownStart[d]
		i := elapsed / n.pd
		o := elapsed % n.pd
		if limit := (i + 1) * n.x; o < min(limit, n.pd) {
			return false, ErrBrownout
		}
		n.recordAccess(d, c, now)
		n.commit(now)
		return true, nil
	default:
		return false, ErrRetired
	}
}

func (n *naiveEngine) ack(c, d string, now int64) error {
	if c == "" || d == "" {
		return ErrInvalidParam
	}
	if err := n.clockOK(now); err != nil {
		return err
	}
	if !n.exists(d) {
		return ErrDatasetNotFound
	}
	if _, ok := n.lastAccess[d][c]; !ok {
		return ErrNotConsumer
	}
	n.acked[d][c] = true
	n.commit(now)
	return nil
}

// 朴素活跃判定：全量扫描所有历史消费者。
func (n *naiveEngine) activeConsumers(d string, now int64) []string {
	out := []string{}
	for c, la := range n.lastAccess[d] {
		if la > now-n.q && !n.acked[d][c] {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func (n *naiveEngine) isActive(d, c string, now int64) bool {
	la, ok := n.lastAccess[d][c]
	return ok && la > now-n.q && !n.acked[d][c]
}

func (n *naiveEngine) advance(d string, now int64) error {
	if d == "" {
		return ErrInvalidParam
	}
	if err := n.clockOK(now); err != nil {
		return err
	}
	if !n.exists(d) {
		return ErrDatasetNotFound
	}
	switch n.phase[d] {
	case 1:
		if now < n.brownStart[d] {
			return ErrTooEarly
		}
		n.phase[d] = 2
		n.commit(now)
		return nil
	case 2:
		if now < n.sunsetAt[d] {
			return ErrTooEarly
		}
		blocked := []string{}
		for _, c := range n.children[d] {
			if n.phase[c] != 3 {
				blocked = append(blocked, c)
			}
		}
		if len(blocked) > 0 {
			sort.Strings(blocked)
			return &ListError{Kind: ErrDownstream, Names: blocked}
		}
		if active := n.activeConsumers(d, now); len(active) > 0 {
			return &ListError{Kind: ErrConsumers, Names: active}
		}
		n.phase[d] = 3
		n.commit(now)
		return nil
	default:
		return ErrPhase
	}
}

func (n *naiveEngine) extend(d, c string, extra, now int64) error {
	if d == "" || c == "" || extra <= 0 {
		return ErrInvalidParam
	}
	if err := n.clockOK(now); err != nil {
		return err
	}
	if !n.exists(d) {
		return ErrDatasetNotFound
	}
	if n.phase[d] != 1 {
		return ErrPhase
	}
	if !n.isActive(d, c, now) {
		return ErrNotConsumer
	}
	if n.extCount[d] >= MaxExtensions {
		return ErrExtendLimit
	}
	if n.extTotal[d]+extra > n.xmax {
		return ErrTooLong
	}
	n.sunsetAt[d] += extra
	n.brownStart[d] += extra
	n.extCount[d]++
	n.extTotal[d] += extra
	n.commit(now)
	return nil
}

type outcome struct {
	kind string
	warn bool
	list []string
}

var sentinels = []struct {
	name string
	err  error
}{
	{"ErrInvalidParam", ErrInvalidParam},
	{"ErrClockRegression", ErrClockRegression},
	{"ErrDatasetNotFound", ErrDatasetNotFound},
	{"ErrDatasetExists", ErrDatasetExists},
	{"ErrPhase", ErrPhase},
	{"ErrNoticeTooShort", ErrNoticeTooShort},
	{"ErrTooEarly", ErrTooEarly},
	{"ErrDownstream", ErrDownstream},
	{"ErrConsumers", ErrConsumers},
	{"ErrBrownout", ErrBrownout},
	{"ErrRetired", ErrRetired},
	{"ErrNotConsumer", ErrNotConsumer},
	{"ErrExtendLimit", ErrExtendLimit},
	{"ErrTooLong", ErrTooLong},
}

func kindOf(err error) string {
	if err == nil {
		return "nil"
	}
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	return fmt.Sprintf("unknown(%v)", err)
}

func listOf(err error) []string {
	var le *ListError
	if errors.As(err, &le) {
		return le.Names
	}
	return nil
}

func normalize(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// randOp 为一条可重放的随机操作。
type randOp struct {
	desc    string
	dataset string
	target  string // consumer
	parents []string
	num     int64
	now     int64
	kind    string // add|deprecate|undeprecate|access|ack|advance|extend
}

func applyEngine(e *Engine, op randOp) outcome {
	switch op.kind {
	case "add":
		return outcome{kind: kindOf(e.AddDataset(op.dataset, op.parents, op.now))}
	case "deprecate":
		list, err := e.Deprecate(op.dataset, op.num, op.now)
		return outcome{kind: kindOf(err), list: list}
	case "undeprecate":
		return outcome{kind: kindOf(e.Undeprecate(op.dataset, op.now))}
	case "access":
		warn, err := e.Access(op.target, op.dataset, op.now)
		return outcome{kind: kindOf(err), warn: warn}
	case "ack":
		return outcome{kind: kindOf(e.Ack(op.target, op.dataset, op.now))}
	case "advance":
		err := e.Advance(op.dataset, op.now)
		return outcome{kind: kindOf(err), list: listOf(err)}
	case "extend":
		return outcome{kind: kindOf(e.Extend(op.dataset, op.target, op.num, op.now))}
	}
	panic("bad op")
}

func applyNaive(n *naiveEngine, op randOp) outcome {
	switch op.kind {
	case "add":
		return outcome{kind: kindOf(n.addDataset(op.dataset, op.parents, op.now))}
	case "deprecate":
		list, err := n.deprecate(op.dataset, op.num, op.now)
		return outcome{kind: kindOf(err), list: list}
	case "undeprecate":
		return outcome{kind: kindOf(n.undeprecate(op.dataset, op.now))}
	case "access":
		warn, err := n.access(op.target, op.dataset, op.now)
		return outcome{kind: kindOf(err), warn: warn}
	case "ack":
		return outcome{kind: kindOf(n.ack(op.target, op.dataset, op.now))}
	case "advance":
		err := n.advance(op.dataset, op.now)
		return outcome{kind: kindOf(err), list: listOf(err)}
	case "extend":
		return outcome{kind: kindOf(n.extend(op.dataset, op.target, op.num, op.now))}
	}
	panic("bad op")
}

func sameOutcome(a, b outcome) bool {
	return a.kind == b.kind && a.warn == b.warn &&
		reflect.DeepEqual(normalize(a.list), normalize(b.list))
}

// 1500 组随机操作序列：Engine 与朴素模拟逐步对照，并在第二个
// Engine 上重放验证确定性；日志打印输入、输出与判定依据。
func TestRandomSequencesMatchNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		nmin := 5 + rng.Int63n(46)
		bw := 1 + rng.Int63n(nmin)
		pd := 1 + rng.Int63n(30)
		x := 1 + rng.Int63n(pd)
		q := 1 + rng.Int63n(40)
		xmax := 1 + rng.Int63n(100)
		eng, err := NewEngine(nmin, bw, pd, x, q, xmax)
		if err != nil {
			t.Fatalf("seq %d: NewEngine: %v", seq, err)
		}
		replay, _ := NewEngine(nmin, bw, pd, x, q, xmax)
		naive := newNaive(nmin, bw, pd, x, q, xmax)
		params := fmt.Sprintf("Nmin=%d Bw=%d Pd=%d X=%d Q=%d Xmax=%d",
			nmin, bw, pd, x, q, xmax)

		var cur int64
		ops := 25 + rng.Intn(25)
		for k := 0; k < ops; k++ {
			op := genOp(rng, naive, nextNow(rng, &cur))
			got := applyEngine(eng, op)
			want := applyNaive(naive, op)
			gotReplay := applyEngine(replay, op)
			t.Logf("seq=%d op=%d %s | %s => kind=%s warn=%v list=%v",
				seq, k, params, op.desc, got.kind, got.warn, got.list)
			if !sameOutcome(got, want) {
				t.Fatalf("seq %d op %d (%s): %s\n got=%+v\nwant=%+v",
					seq, k, params, op.desc, got, want)
			}
			if !sameOutcome(gotReplay, got) {
				t.Fatalf("seq %d op %d (%s): replay mismatch on %s: %+v vs %+v",
					seq, k, params, op.desc, gotReplay, got)
			}
		}
	}
}

func nextNow(rng *rand.Rand, cur *int64) int64 {
	switch r := rng.Intn(100); {
	case r < 5: // 时钟回退
		return *cur - rng.Int63n(30)
	case r < 7: // 越界
		return maxTimestamp + 1 + rng.Int63n(10)
	case r < 9: // 负值
		return -1 - rng.Int63n(10)
	default:
		*cur += rng.Int63n(40)
		return *cur
	}
}

func genOp(rng *rand.Rand, naive *naiveEngine, now int64) randOp {
	datasets := []string{"d0", "d1", "d2", "d3", "d4", "d5"}
	consumers := []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"}
	var existing []string
	for _, name := range datasets {
		if naive.exists(name) {
			existing = append(existing, name)
		}
	}
	d := datasets[rng.Intn(len(datasets))]
	if len(existing) > 0 && rng.Intn(100) < 80 {
		d = existing[rng.Intn(len(existing))]
	}
	c := consumers[rng.Intn(len(consumers))]
	if rng.Intn(50) == 0 {
		c = "" // 非法消费者名
	}
	op := randOp{dataset: d, target: c, now: now}
	roll := rng.Intn(100)
	if len(existing) == 0 {
		roll = 0 // 首个操作必须是 add
	}
	switch {
	case roll < 10: // add
		op.kind = "add"
		op.parents = randomParents(rng, existing, datasets)
		op.desc = fmt.Sprintf("AddDataset(%s,%v,%d)", d, op.parents, now)
	case roll < 25: // deprecate
		op.kind = "deprecate"
		op.num = rng.Int63n(2*naive.nmin + 20)
		op.desc = fmt.Sprintf("Deprecate(%s,%d,%d)", d, op.num, now)
	case roll < 30: // undeprecate
		op.kind = "undeprecate"
		op.desc = fmt.Sprintf("Undeprecate(%s,%d)", d, now)
	case roll < 60: // access
		op.kind = "access"
		op.desc = fmt.Sprintf("Access(%s,%s,%d)", c, d, now)
	case roll < 68: // ack
		op.kind = "ack"
		if rc, ok := naive.someConsumer(d); ok && rng.Intn(100) < 70 {
			op.target = rc
		}
		op.desc = fmt.Sprintf("Ack(%s,%s,%d)", op.target, d, now)
	case roll < 85: // advance
		op.kind = "advance"
		// 一半概率把时间点推到阶段边界，加深生命周期覆盖。
		if naive.exists(d) && rng.Intn(2) == 0 {
			switch naive.phase[d] {
			case 1:
				if naive.brownStart[d] >= now {
					op.now = naive.brownStart[d]
				}
			case 2:
				if naive.sunsetAt[d] >= now {
					op.now = naive.sunsetAt[d]
				}
			}
		}
		op.desc = fmt.Sprintf("Advance(%s,%d)", d, op.now)
	default: // extend
		op.kind = "extend"
		op.num = rng.Int63n(naive.xmax+10) - 2
		if rc, ok := naive.recentConsumer(d, now); ok && rng.Intn(100) < 70 {
			op.target = rc
		}
		op.desc = fmt.Sprintf("Extend(%s,%s,%d,%d)", d, op.target, op.num, now)
	}
	return op
}

func randomParents(rng *rand.Rand, existing, datasets []string) []string {
	n := rng.Intn(10) // 0..9，覆盖超过 8 的非法情形
	parents := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var p string
		switch r := rng.Intn(100); {
		case r < 5:
			p = "ghost"
		case r < 10 || len(existing) == 0:
			p = datasets[rng.Intn(len(datasets))]
		default:
			p = existing[rng.Intn(len(existing))]
		}
		parents = append(parents, p)
	}
	return parents
}

// someConsumer 返回对 d 有过成功访问的任一消费者。
func (n *naiveEngine) someConsumer(d string) (string, bool) {
	for c := range n.lastAccess[d] {
		return c, true
	}
	return "", false
}

// recentConsumer 返回 d 的任一活跃消费者。
func (n *naiveEngine) recentConsumer(d string, now int64) (string, bool) {
	for c, la := range n.lastAccess[d] {
		if la > now-n.q && !n.acked[d][c] {
			return c, true
		}
	}
	return "", false
}
