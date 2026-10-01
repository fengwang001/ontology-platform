package waterfall

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
)

// mustNew 构造分账器，配置非法时终止测试。
func mustNew(t *testing.T, layers []Layer) *Waterfall {
	t.Helper()
	w, err := New(layers)
	if err != nil {
		t.Fatalf("New(%v) unexpected error: %v", layers, err)
	}
	return w
}

// naiveModel 是完全按题目规则写成的逐步朴素模拟，独立于实现用于对照。
type naiveModel struct {
	caps []int64 // 最后一层为 -1 表示无上限
	recv []int64
	fee  int
}

func newNaive(caps []int64, fee int) *naiveModel {
	return &naiveModel{
		caps: append([]int64(nil), caps...),
		recv: make([]int64, len(caps)),
		fee:  fee,
	}
}

func (m *naiveModel) total() int64 {
	var sum int64
	for _, r := range m.recv {
		sum += r
	}
	return sum
}

func (m *naiveModel) allocate(x int64) {
	remaining := x
	for i := 0; i < len(m.recv)-1; i++ {
		fill := min(remaining, m.caps[i]-m.recv[i])
		m.recv[i] += fill
		remaining -= fill
	}
	m.recv[len(m.recv)-1] += remaining
}

func (m *naiveModel) clawback(y int64) {
	remaining := y
	for i := len(m.recv) - 1; i >= 0; i-- {
		cut := min(remaining, m.recv[i])
		m.recv[i] -= cut
		remaining -= cut
	}
}

func (m *naiveModel) split() (manager, investor int64) {
	r := m.recv[len(m.recv)-1]
	manager = r * int64(m.fee) / 100
	return manager, r - manager
}

// assertAgainst 对照实现与朴素模拟的全部可观察结果，并校验内部不变量。
func assertAgainst(t *testing.T, w *Waterfall, m *naiveModel, label string) {
	t.Helper()
	got := w.Snapshot()
	if fmt.Sprint(got) != fmt.Sprint(m.recv) {
		t.Errorf("%s: layers mismatch: got %v, want %v (naive)", label, got, m.recv)
	}
	if w.Total() != m.total() {
		t.Errorf("%s: total mismatch: got %d, want %d", label, w.Total(), m.total())
	}
	gm, gi := w.Split()
	wm, wi := m.split()
	if gm != wm || gi != wi {
		t.Errorf("%s: split mismatch: got (%d,%d), want (%d,%d) (naive)", label, gm, gi, wm, wi)
	}
	var sum int64
	for i, r := range got {
		sum += r
		if i < len(got)-1 && r > m.caps[i] {
			t.Errorf("%s: layer %d recv %d exceeds cap %d", label, i, r, m.caps[i])
		}
	}
	if sum != m.total() {
		t.Errorf("%s: invariant sum of recv %d != total %d", label, sum, m.total())
	}
	t.Logf("%s: layers=%v total=%d split(manager=%d investor=%d)", label, got, m.total(), gm, gi)
}

func TestNewConfigValidation(t *testing.T) {
	cases := []struct {
		name   string
		layers []Layer
		want   error
	}{
		{"nil layers", nil, ErrInvalidConfig},
		{"one layer", []Layer{{Cap: 100, Fee: 10}}, ErrInvalidConfig},
		{"negative cap", []Layer{{Cap: -1}, {Fee: 10}}, ErrNegativeCap},
		{"fee negative", []Layer{{Cap: 10}, {Fee: -1}}, ErrFeeOutOfRange},
		{"fee over 100", []Layer{{Cap: 10}, {Fee: 101}}, ErrFeeOutOfRange},
		{"valid zero cap zero fee", []Layer{{Cap: 0}, {Fee: 0}}, nil},
		{"valid fee 100", []Layer{{Cap: 10}, {Fee: 100}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, err := New(c.layers)
			decision := "接受（配置合法）"
			if c.want != nil {
				decision = fmt.Sprintf("拒绝，原因 %v（符合预期=%v）", err, errors.Is(err, c.want))
			}
			t.Logf("输入 layers=%v -> 输出 err=%v, 判定依据: %s", c.layers, err, decision)
			if c.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("got err %v, want %v", err, c.want)
			}
			_ = w
		})
	}
}

func TestAllocateExactFillAndCrossLayers(t *testing.T) {
	// caps: [10, 20, 余额层 g=20]
	w := mustNew(t, []Layer{{Cap: 10}, {Cap: 20}, {Fee: 20}})
	m := newNaive([]int64{10, 20, -1}, 20)

	if err := w.Allocate(10); err != nil {
		t.Fatalf("allocate 10: %v", err)
	}
	m.allocate(10)
	assertAgainst(t, w, m, "输入 allocate(10): 恰好填满第0层")

	if err := w.Allocate(25); err != nil {
		t.Fatalf("allocate 25: %v", err)
	}
	m.allocate(25) // 第1层吃15，余5进余额层
	assertAgainst(t, w, m, "输入 allocate(25): 一笔跨多层, 第1层填满20余5进末层")
	if got := w.Snapshot(); fmt.Sprint(got) != "[10 20 5]" {
		t.Fatalf("unexpected layers: %v", got)
	}

	if err := w.Allocate(100); err != nil {
		t.Fatalf("allocate 100: %v", err)
	}
	m.allocate(100) // 第1层再吃5填满，95进余额层 => 末层 100
	assertAgainst(t, w, m, "输入 allocate(100): 前两层已满, 100全部进末层, 末层累计105")
	if got := w.Snapshot(); fmt.Sprint(got) != "[10 20 105]" {
		t.Fatalf("unexpected layers: %v", got)
	}
	mg, inv := w.Split()
	t.Logf("输出 split: manager=%d investor=%d, 判定依据: floor(105*20/100)=21", mg, inv)
	if mg != 21 || inv != 84 {
		t.Fatalf("split = (%d,%d), want (21,84)", mg, inv)
	}
}

func TestClawbackLastLayerOnly(t *testing.T) {
	w0 := mustNew(t, []Layer{{Cap: 10}, {Cap: 20}, {Fee: 20}})
	m0 := newNaive([]int64{10, 20, -1}, 20)
	if err := w0.Allocate(45); err != nil {
		t.Fatalf("allocate 45: %v", err)
	}
	m0.allocate(45) // 第0层10、第1层20、末层15
	assertAgainst(t, w0, m0, "输入 allocate(45): 单笔跨满三层, 10+20+15")
	if got := w0.Snapshot(); fmt.Sprint(got) != "[10 20 15]" {
		t.Fatalf("unexpected layers: %v", got)
	}

	w := mustNew(t, []Layer{{Cap: 10}, {Cap: 20}, {Fee: 20}})
	m := newNaive([]int64{10, 20, -1}, 20)
	w.Allocate(50)
	m.allocate(50) // [10 20 20]

	if err := w.Clawback(5); err != nil {
		t.Fatalf("clawback 5: %v", err)
	}
	m.clawback(5) // 末层 20 -> 15
	assertAgainst(t, w, m, "输入 clawback(5): 追回只扣末层")
	if mg, _ := w.Split(); mg != 3 {
		t.Fatalf("manager = %d, want floor(15*20/100)=3", mg)
	}
}

func TestClawbackReverseAcrossLayers(t *testing.T) {
	w := mustNew(t, []Layer{{Cap: 10}, {Cap: 20}, {Fee: 20}})
	m := newNaive([]int64{10, 20, -1}, 20)
	w.Allocate(50)
	m.allocate(50) // [10 20 20]

	if err := w.Clawback(35); err != nil {
		t.Fatalf("clawback 35: %v", err)
	}
	m.clawback(35) // 末层扣20，第1层扣15 => [10 5 0]
	assertAgainst(t, w, m, "输入 clawback(35): 跨层逆序追回, 末层20+第1层15")
	if got := w.Snapshot(); fmt.Sprint(got) != "[10 5 0]" {
		t.Fatalf("unexpected layers: %v", got)
	}
	if w.Total() != 15 {
		t.Fatalf("total = %d, want 15", w.Total())
	}
}

func TestReallocateFillsFromFirstAvailableLayer(t *testing.T) {
	w := mustNew(t, []Layer{{Cap: 10}, {Cap: 20}, {Fee: 20}})
	m := newNaive([]int64{10, 20, -1}, 20)
	apply := func(desc string, op func() error, naive func()) {
		t.Helper()
		if err := op(); err != nil {
			t.Fatalf("%s: %v", desc, err)
		}
		naive()
		assertAgainst(t, w, m, desc)
	}

	apply("输入 allocate(50)", func() error { return w.Allocate(50) }, func() { m.allocate(50) })
	apply("输入 clawback(25): 清空末层再扣第1层5", func() error { return w.Clawback(25) }, func() { m.clawback(25) })
	// 当前 [10 15 0]；再分配必须从第一个有空位的层（第1层）补起，而非灌入末层。
	apply("输入 allocate(10): 追回后再分配先补第1层空位5, 余5进末层",
		func() error { return w.Allocate(10) }, func() { m.allocate(10) })
	if got := w.Snapshot(); fmt.Sprint(got) != "[10 20 5]" {
		t.Fatalf("unexpected layers: %v, 再分配未从第一个有空位的层开始", got)
	}
}

func TestCumulativeVsPerInstallmentSplit(t *testing.T) {
	// g=20：余额层先后收到 4 与 4。
	// 逐笔口径：floor(4*20/100)+floor(4*20/100) = 0。
	// 累计口径：floor(8*20/100) = 1。
	w := mustNew(t, []Layer{{Cap: 10}, {Fee: 20}})
	m := newNaive([]int64{10, -1}, 20)

	w.Allocate(10 + 4)
	m.allocate(10 + 4) // [10 4]
	mg1, _ := w.Split()
	perFirst := int64(4) * 20 / 100
	t.Logf("第一笔后: R=4, 累计口径 manager=%d, 逐笔口径 floor(4*20/100)=%d", mg1, perFirst)
	if mg1 != 0 || perFirst != 0 {
		t.Fatalf("after first installment manager=%d per=%d, want 0/0", mg1, perFirst)
	}

	w.Allocate(4)
	m.allocate(4) // [10 8]
	mg2, inv2 := w.Split()
	cumulative := int64(8) * 20 / 100
	perTotal := perFirst + int64(4)*20/100
	t.Logf("第二笔后: R=8, 累计口径 floor(8*20/100)=%d, 逐笔口径=%d, 判定依据: 必须按累计R重算",
		mg2, perTotal)
	if cumulative != 1 || mg2 != cumulative {
		t.Fatalf("cumulative manager=%d, want 1", mg2)
	}
	if perTotal != 0 {
		t.Fatalf("per-installment = %d, want 0", perTotal)
	}
	if mg2 == perTotal {
		t.Fatal("累计口径与逐笔口径应相差 1，但二者相等")
	}
	if inv2 != 7 {
		t.Fatalf("investor = %d, want 7", inv2)
	}
	assertAgainst(t, w, m, "累计拆分口径: 4+4 => manager 1")
}

func TestClawbackReducesManagerFee(t *testing.T) {
	w := mustNew(t, []Layer{{Cap: 10}, {Fee: 20}})
	if err := w.Allocate(20); err != nil { // [10 10]
		t.Fatalf("allocate: %v", err)
	}
	before, _ := w.Split() // floor(10*20/100) = 2
	if err := w.Clawback(5); err != nil {
		t.Fatalf("clawback: %v", err)
	}
	after, _ := w.Split() // 末层 10 -> 5，floor(5*20/100) = 1
	t.Logf("追回使管理人所得下降: manager %d -> %d (R 10 -> 5), 判定依据: 追回后按累计R重算", before, after)
	if before != 2 || after != 1 {
		t.Fatalf("manager before=%d after=%d, want 2 -> 1", before, after)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	w := mustNew(t, []Layer{{Cap: 10}, {Fee: 20}})
	if err := w.Allocate(5); err != nil { // [5 0]
		t.Fatalf("allocate: %v", err)
	}
	before := w.Snapshot()

	// 金额不为正：必须先于「追回超过总额」判定。
	if err := w.Allocate(0); !errors.Is(err, ErrAmountNotPositive) {
		t.Fatalf("allocate(0) err=%v, want ErrAmountNotPositive", err)
	}
	if err := w.Allocate(-3); !errors.Is(err, ErrAmountNotPositive) {
		t.Fatalf("allocate(-3) err=%v, want ErrAmountNotPositive", err)
	}
	if err := w.Clawback(0); !errors.Is(err, ErrAmountNotPositive) {
		t.Fatalf("clawback(0) err=%v, want ErrAmountNotPositive", err)
	}
	// 追回 100 既超过总额(5)，但金额为正 -> 另一种原因。
	if err := w.Clawback(100); !errors.Is(err, ErrClawbackExceedsRecv) {
		t.Fatalf("clawback(100) err=%v, want ErrClawbackExceedsRecv", err)
	}
	// 分配后总额将超过 1e15。
	big := mustNew(t, []Layer{{Cap: 0}, {Fee: 0}})
	if err := big.Allocate(MaxTotal); err != nil {
		t.Fatalf("allocate up to limit: %v", err)
	}
	if err := big.Allocate(1); !errors.Is(err, ErrTotalExceedsLimit) {
		t.Fatalf("allocate over 1e15 err=%v, want ErrTotalExceedsLimit", err)
	}
	if err := big.Allocate(MaxTotal + 1); !errors.Is(err, ErrTotalExceedsLimit) {
		t.Fatalf("allocate single overflow err=%v, want ErrTotalExceedsLimit", err)
	}

	after := w.Snapshot()
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("被拒绝的操作改变了已收额: before=%v after=%v", before, after)
	}
	bigAfter := big.Snapshot()
	if fmt.Sprint(bigAfter) != fmt.Sprint([]int64{0, MaxTotal}) {
		t.Fatalf("被拒绝的溢出分配改变了已收额: %v", bigAfter)
	}
	t.Logf("输入非法操作序列(0/-3/0/100/溢出) -> 全部拒绝, 状态不变 layers=%v big=%v, 判定依据: 锁内校验失败直接返回", after, bigAfter)
}

// TestNaiveFuzzReplay 随机操作序列重放：实现必须与朴素模拟逐步一致，
// 且同一序列重放两次得到完全相同的各层已收额与拆分结果。
func TestNaiveFuzzReplay(t *testing.T) {
	seed := uint64(20261001)
	run := func() [][3]int64 {
		rng := rand.New(rand.NewPCG(seed, seed))
		caps := []int64{10, 25, 50, -1}
		w := mustNew(t, []Layer{{Cap: 10}, {Cap: 25}, {Cap: 50}, {Fee: 37}})
		m := newNaive(caps, 37)
		var trace [][3]int64 // [op, manager, investor]
		for step := 0; step < 2000; step++ {
			if rng.IntN(5) < 3 || m.total() == 0 {
				x := int64(rng.IntN(80)) + 1 // 1..80
				err := w.Allocate(x)
				if m.total()+x > MaxTotal {
					if !errors.Is(err, ErrTotalExceedsLimit) {
						t.Fatalf("step %d allocate %d: want overflow, got %v", step, x, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("step %d allocate %d: %v", step, x, err)
				}
				m.allocate(x)
			} else {
				y := int64(rng.IntN(int(m.total()) + 5))
				err := w.Clawback(y)
				if y <= 0 {
					if !errors.Is(err, ErrAmountNotPositive) {
						t.Fatalf("step %d clawback %d: want not-positive, got %v", step, y, err)
					}
					continue
				}
				if y > m.total() {
					if !errors.Is(err, ErrClawbackExceedsRecv) {
						t.Fatalf("step %d clawback %d: want exceeds, got %v", step, y, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("step %d clawback %d: %v", step, y, err)
				}
				m.clawback(y)
			}
			mg, inv := w.Split()
			trace = append(trace, [3]int64{int64(step), mg, inv})
			assertAgainst(t, w, m, fmt.Sprintf("随机序列 step=%d", step))
		}
		return trace
	}
	first := run()
	second := run()
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("相同操作序列重放结果不同: first=%v second=%v", first, second)
	}
	t.Logf("确定性重放: %d 次有效操作的分层与拆分序列两次完全一致", len(first))
}

// TestConcurrentLinearizable 并发调用分配、追回与查询：
// 串行等价性体现为最终总额等于净注入额，且每一层始终满足不变量。
func TestConcurrentLinearizable(t *testing.T) {
	w := mustNew(t, []Layer{{Cap: 100}, {Cap: 200}, {Fee: 20}})
	var wg sync.WaitGroup
	var net int64
	var netMu sync.Mutex

	// 先注入足够本金，保证追回并发时不会长期因超额被拒。
	for range 40 {
		if err := w.Allocate(10); err != nil {
			t.Fatalf("seed allocate: %v", err)
		}
		netMu.Lock()
		net += 10
		netMu.Unlock()
	}

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if err := w.Allocate(3); err == nil {
					netMu.Lock()
					net += 3
					netMu.Unlock()
				}
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if err := w.Clawback(2); err == nil {
					netMu.Lock()
					net -= 2
					netMu.Unlock()
				}
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				recv, total, mg, inv := w.State()
				var sum int64
				for _, r := range recv {
					sum += r
				}
				if sum != total || recv[0] > 100 || recv[1] > 200 || mg+inv != recv[2] {
					t.Errorf("并发查询观测到不一致状态: recv=%v total=%d split=(%d,%d)", recv, total, mg, inv)
					return
				}
			}
		}()
	}
	wg.Wait()

	recv := w.Snapshot()
	var sum int64
	for _, r := range recv {
		sum += r
	}
	netMu.Lock()
	wantNet := net
	netMu.Unlock()
	if sum != wantNet || w.Total() != wantNet {
		t.Fatalf("并发结束后 sum=%d total=%d, want net=%d (等价于某个串行顺序)", sum, w.Total(), wantNet)
	}
	mg, inv := w.Split()
	t.Logf("并发结束: layers=%v total=%d split(manager=%d investor=%d), 判定依据: 总和=净注入%d",
		recv, sum, mg, inv, wantNet)
}
