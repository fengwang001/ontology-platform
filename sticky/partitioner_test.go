package sticky

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naivePartitioner 是严格按题目规则逐条书写的朴素模拟，
// 独立于生产实现，作为对照基准（单线程，不做任何优化）。
type naivePartitioner struct {
	n         int
	b         int
	available []bool
	sticky    int
	acc       int
	log       bytes.Buffer
}

func newNaive(n, b int) *naivePartitioner {
	av := make([]bool, n)
	for i := range av {
		av[i] = true
	}
	m := &naivePartitioner{n: n, b: b, available: av, sticky: 0, acc: 0}
	fmt.Fprintf(&m.log, "naive-init N=%d B=%d sticky=0 acc=0\n", n, b)
	return m
}

// naiveFNV 与生产代码独立书写的 FNV-1a 实现。
func naiveFNV(key []byte) uint32 {
	var h uint32 = 2166136261
	for _, c := range key {
		h = h ^ uint32(c)
		h = h * 16777619
	}
	return h
}

func (m *naivePartitioner) sendKeyed(key []byte) int {
	p := int(naiveFNV(key) % uint32(m.n))
	fmt.Fprintf(&m.log, "naive-keyed key=%q -> %d acc=%d unchanged\n", key, p, m.acc)
	return p
}

func (m *naivePartitioner) sendKeyless(s int) (int, error) {
	if s < 1 {
		fmt.Fprintf(&m.log, "naive-keyless REJECT s=%d size<1 state-unchanged\n", s)
		return 0, ErrInvalidMessageSize
	}
	any := false
	for _, ok := range m.available {
		if ok {
			any = true
		}
	}
	if !any {
		fmt.Fprintf(&m.log, "naive-keyless REJECT s=%d no-available state-unchanged sticky=%d\n", s, m.sticky)
		return 0, ErrNoAvailablePartition
	}
	if !m.available[m.sticky] {
		target := -1
		for step := 1; step <= m.n; step++ {
			c := (m.sticky + step) % m.n
			if m.available[c] {
				target = c
				break
			}
		}
		fmt.Fprintf(&m.log, "naive-keyless send-time switch %d->%d acc %d->0\n", m.sticky, target, m.acc)
		m.sticky = target
		m.acc = 0
	}
	part := m.sticky
	m.acc += s
	if m.acc >= m.b {
		next := -1
		for step := 1; step <= m.n; step++ {
			c := (part + step) % m.n
			if m.available[c] {
				next = c
				break
			}
		}
		if next >= 0 {
			fmt.Fprintf(&m.log, "naive-keyless s=%d part=%d acc=%d>=B switch %d->%d reset\n", s, part, m.acc, part, next)
			m.sticky = next
		} else {
			fmt.Fprintf(&m.log, "naive-keyless s=%d part=%d acc=%d>=B only-current stay reset\n", s, part, m.acc)
		}
		m.acc = 0
	} else {
		fmt.Fprintf(&m.log, "naive-keyless s=%d part=%d acc=%d stay\n", s, part, m.acc)
	}
	return part, nil
}

func (m *naivePartitioner) setAvailable(p int, ok bool) error {
	if p < 0 || p >= m.n {
		fmt.Fprintf(&m.log, "naive-set REJECT p=%d out-of-range state-unchanged\n", p)
		return ErrPartitionOutOfRange
	}
	m.available[p] = ok
	fmt.Fprintf(&m.log, "naive-set p=%d available=%v sticky=%d acc=%d no-switch\n", p, ok, m.sticky, m.acc)
	return nil
}

// op 是一次可重放的调用记录。
type op struct {
	kind      string // "keyed" / "keyless" / "set"
	key       []byte
	size      int
	partition int
	available bool
}

type opResult struct {
	partition int
	err       error
}

func replayNaive(t *testing.T, n, b int, ops []op) []opResult {
	t.Helper()
	m := newNaive(n, b)
	results := make([]opResult, len(ops))
	for i, o := range ops {
		switch o.kind {
		case "keyed":
			results[i] = opResult{partition: m.sendKeyed(o.key)}
		case "keyless":
			p, err := m.sendKeyless(o.size)
			results[i] = opResult{partition: p, err: err}
		case "set":
			results[i] = opResult{err: m.setAvailable(o.partition, o.available)}
		default:
			t.Fatalf("unknown op kind %q", o.kind)
		}
	}
	t.Logf("naive simulation trace:\n%s", m.log.String())
	return results
}

func replayReal(t *testing.T, n, b int, ops []op) ([]opResult, *Partitioner) {
	t.Helper()
	var buf bytes.Buffer
	p, err := New(n, b)
	if err != nil {
		t.Fatalf("New(%d,%d) unexpected error: %v", n, b, err)
	}
	p.SetLogger(&buf)
	results := make([]opResult, len(ops))
	for i, o := range ops {
		switch o.kind {
		case "keyed":
			results[i] = opResult{partition: p.SendKeyed(o.key)}
		case "keyless":
			part, err := p.SendKeyless(o.size)
			results[i] = opResult{partition: part, err: err}
		case "set":
			results[i] = opResult{err: p.SetAvailable(o.partition, o.available)}
		}
	}
	t.Logf("real partitioner trace:\n%s", buf.String())
	return results, p
}

func assertResultsEqual(t *testing.T, want, got []opResult) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("result length mismatch: want %d got %d", len(want), len(got))
	}
	for i := range want {
		if want[i].partition != got[i].partition || want[i].err != got[i].err {
			t.Fatalf("op[%d] mismatch: want {part=%d err=%v} got {part=%d err=%v}",
				i, want[i].partition, want[i].err, got[i].partition, got[i].err)
		}
	}
}

func TestExactThresholdSwitchesAfterMessage(t *testing.T) {
	// 累计恰等于 B：本条仍发当前分区，本条之后才切换。
	ops := []op{
		{kind: "keyless", size: 4},
		{kind: "keyless", size: 6}, // 累计 4+6=10=B，本条在分区 0，之后切到 1
		{kind: "keyless", size: 3}, // 新批次，分区 1，累计 3
	}
	want := replayNaive(t, 4, 10, ops)
	got, p := replayReal(t, 4, 10, ops)
	assertResultsEqual(t, want, got)
	if got[1].partition != 0 || got[2].partition != 1 {
		t.Fatalf("exact-B timing wrong: %+v %+v", got[1], got[2])
	}
	if p.StickyPartition() != 1 || p.AccumulatedBytes() != 3 {
		t.Fatalf("state after exact-B batch: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}
}

func TestSingleMessageLargerThanB(t *testing.T) {
	// 单条大于 B：本条发当前分区，之后立即切换并清零。
	ops := []op{
		{kind: "keyless", size: 100},
		{kind: "keyless", size: 1},
	}
	want := replayNaive(t, 3, 10, ops)
	got, p := replayReal(t, 3, 10, ops)
	assertResultsEqual(t, want, got)
	if got[0].partition != 0 || got[1].partition != 1 {
		t.Fatalf("single-large timing wrong: %+v %+v", got[0], got[1])
	}
	if p.StickyPartition() != 1 || p.AccumulatedBytes() != 1 {
		t.Fatalf("state after single-large: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}
}

func TestRingSkipsUnavailable(t *testing.T) {
	// 达到 B 时环形跳过不可用分区。
	ops := []op{
		{kind: "set", partition: 1, available: false},
		{kind: "keyless", size: 10}, // 分区 0，累计=B，下一个可用跳过 1 -> 2
		{kind: "keyless", size: 2},  // 分区 2，累计 2
		{kind: "set", partition: 0, available: false},
		{kind: "keyless", size: 10}, // 分区 2 累计=B，环形 3、0(否)、1(否) -> 3
		{kind: "keyless", size: 1},  // 分区 3
	}
	want := replayNaive(t, 4, 10, ops)
	got, p := replayReal(t, 4, 10, ops)
	assertResultsEqual(t, want, got)
	if got[1].partition != 0 || got[2].partition != 2 || got[4].partition != 2 || got[5].partition != 3 {
		t.Fatalf("ring skip sequence wrong: %+v", got)
	}

	// 只有当前分区可用：达 B 后仍留在当前分区，累计清零。
	ops = []op{
		{kind: "set", partition: 1, available: false},
		{kind: "set", partition: 2, available: false},
		{kind: "set", partition: 3, available: false},
		{kind: "keyless", size: 10},
		{kind: "keyless", size: 7},
	}
	want = replayNaive(t, 4, 10, ops)
	got, p = replayReal(t, 4, 10, ops)
	assertResultsEqual(t, want, got)
	if got[3].partition != 0 || got[4].partition != 0 || p.StickyPartition() != 0 || p.AccumulatedBytes() != 7 {
		t.Fatalf("only-current-available stay wrong: %+v sticky=%d acc=%d", got[4], p.StickyPartition(), p.AccumulatedBytes())
	}
}

func TestStickyMarkedUnavailableSwitchTiming(t *testing.T) {
	// 标记不可用本身不切换、不重置；仅在下一条无键消息发送时切换。
	ops := []op{
		{kind: "keyless", size: 4}, // 分区 0，累计 4
		{kind: "set", partition: 0, available: false},
	}
	want := replayNaive(t, 3, 10, ops)
	got, p := replayReal(t, 3, 10, ops)
	assertResultsEqual(t, want, got)
	if p.StickyPartition() != 0 || p.AccumulatedBytes() != 4 {
		t.Fatalf("set-unavailable must not switch/reset: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}

	// 下一条发送时刻才切到环形第一个可用分区（1），旧累计作废。
	ops = append(ops, op{kind: "keyless", size: 2})
	want = replayNaive(t, 3, 10, ops)
	got, p = replayReal(t, 3, 10, ops)
	assertResultsEqual(t, want, got)
	if got[2].partition != 1 || p.StickyPartition() != 1 || p.AccumulatedBytes() != 2 {
		t.Fatalf("send-time switch wrong: part=%d sticky=%d acc=%d", got[2].partition, p.StickyPartition(), p.AccumulatedBytes())
	}

	// 发送时没有任何可用分区：拒绝且状态不变。
	ops = []op{
		{kind: "set", partition: 0, available: false},
		{kind: "set", partition: 1, available: false},
		{kind: "set", partition: 2, available: false},
		{kind: "keyless", size: 5},
	}
	want = replayNaive(t, 3, 10, ops)
	got, p = replayReal(t, 3, 10, ops)
	assertResultsEqual(t, want, got)
	if got[3].err != ErrNoAvailablePartition {
		t.Fatalf("want ErrNoAvailablePartition, got %v", got[3].err)
	}
	if p.StickyPartition() != 0 || p.AccumulatedBytes() != 0 {
		t.Fatalf("rejected send must preserve state: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}
}

func TestRecoveredDoesNotSwitchBack(t *testing.T) {
	// 标记不可用后、下一条发送前恢复：不切换，累计保留。
	ops := []op{
		{kind: "keyless", size: 4},
		{kind: "set", partition: 0, available: false},
		{kind: "set", partition: 0, available: true},
		{kind: "keyless", size: 5}, // 仍在分区 0，累计 4+5=9
	}
	want := replayNaive(t, 3, 10, ops)
	got, p := replayReal(t, 3, 10, ops)
	assertResultsEqual(t, want, got)
	if got[3].partition != 0 || p.StickyPartition() != 0 || p.AccumulatedBytes() != 9 {
		t.Fatalf("recovery before next send must not switch; part=%d sticky=%d acc=%d", got[3].partition, p.StickyPartition(), p.AccumulatedBytes())
	}

	// 已因发送时刻切走后，旧分区恢复也不回切。
	ops = []op{
		{kind: "keyless", size: 4},
		{kind: "set", partition: 0, available: false},
		{kind: "keyless", size: 2}, // 发送时刻切到 1，累计 2
		{kind: "set", partition: 0, available: true},
		{kind: "keyless", size: 1}, // 仍在 1，累计 3，不回切
	}
	want = replayNaive(t, 3, 10, ops)
	got, p = replayReal(t, 3, 10, ops)
	assertResultsEqual(t, want, got)
	if p.StickyPartition() != 1 || p.AccumulatedBytes() != 3 {
		t.Fatalf("no switch-back after recovery: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}
}

func TestKeyedIgnoresAvailabilityAndAccumulation(t *testing.T) {
	keyHello := []byte("hello")
	ops := []op{
		{kind: "keyless", size: 4}, // 分区 0，累计 4
		{kind: "set", partition: 0, available: false},
		{kind: "set", partition: 1, available: false},
		{kind: "set", partition: 2, available: false},
		// 所有分区不可用时，带键消息仍按哈希返回，且不改变粘性状态。
		{kind: "keyed", key: keyHello},
		{kind: "keyed", key: []byte{}},
		{kind: "set", partition: 0, available: true},
		{kind: "set", partition: 1, available: true},
		{kind: "set", partition: 2, available: true},
		{kind: "keyed", key: keyHello}, // 可用性变化后结果完全相同
	}
	want := replayNaive(t, 3, 10, ops)
	got, p := replayReal(t, 3, 10, ops)
	assertResultsEqual(t, want, got)
	if got[4].partition != got[9].partition {
		t.Fatalf("keyed partition changed with availability: %d vs %d", got[4].partition, got[9].partition)
	}
	if p.StickyPartition() != 0 || p.AccumulatedBytes() != 4 {
		t.Fatalf("keyed messages must not touch sticky state: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}

	// 带键结果只取决于键与 N：不同实例、不同 B 结果一致；与朴素哈希一致。
	p1, _ := New(3, 10)
	p2, _ := New(3, 9999)
	if p1.SendKeyed(keyHello) != p2.SendKeyed(keyHello) {
		t.Fatal("keyed partition must be independent of B")
	}
	p3, _ := New(7, 10)
	if got := p3.SendKeyed(keyHello); got != int(naiveFNV(keyHello)%7) {
		t.Fatalf("N=7 keyed partition mismatch: %d", got)
	}
	if p1.SendKeyed(nil) != int(naiveFNV(nil)%3) {
		t.Fatal("nil key must hash like empty key")
	}
}

func TestFNV1aKnownVectors(t *testing.T) {
	cases := []struct {
		key  string
		hash uint32
	}{
		{"", 0x811c9dc5},
		{"a", 0xe40c292c},
		{"foobar", 0xbf9cf968},
	}
	for _, c := range cases {
		if got := fnv1a32([]byte(c.key)); got != c.hash {
			t.Fatalf("fnv1a32(%q)=%08x want %08x", c.key, got, c.hash)
		}
		if got := naiveFNV([]byte(c.key)); got != c.hash {
			t.Fatalf("naiveFNV(%q)=%08x want %08x", c.key, got, c.hash)
		}
	}
}

func TestRejectsDoNotChangeState(t *testing.T) {
	if _, err := New(0, 10); err != ErrInvalidPartitionCount {
		t.Fatalf("N=0 want ErrInvalidPartitionCount, got %v", err)
	}
	if _, err := New(-3, 10); err != ErrInvalidPartitionCount {
		t.Fatalf("N<0 want ErrInvalidPartitionCount, got %v", err)
	}
	if _, err := New(3, 0); err != ErrInvalidBatchThreshold {
		t.Fatalf("B=0 want ErrInvalidBatchThreshold, got %v", err)
	}

	p, _ := New(3, 10)
	var stateLog bytes.Buffer
	p.SetLogger(&stateLog)

	if _, err := p.SendKeyless(0); err != ErrInvalidMessageSize {
		t.Fatalf("s=0 want ErrInvalidMessageSize, got %v", err)
	}
	if err := p.SetAvailable(-1, false); err != ErrPartitionOutOfRange {
		t.Fatalf("partition -1 want ErrPartitionOutOfRange, got %v", err)
	}
	if err := p.SetAvailable(3, false); err != ErrPartitionOutOfRange {
		t.Fatalf("partition 3 want ErrPartitionOutOfRange, got %v", err)
	}
	if p.StickyPartition() != 0 || p.AccumulatedBytes() != 0 {
		t.Fatalf("rejects changed initial state: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}

	// 构造非零状态后再次拒绝，确认状态不变。
	p.SendKeyless(6)
	if err := p.SetAvailable(99, true); err != ErrPartitionOutOfRange {
		t.Fatalf("want ErrPartitionOutOfRange, got %v", err)
	}
	if _, err := p.SendKeyless(-5); err != ErrInvalidMessageSize {
		t.Fatalf("want ErrInvalidMessageSize, got %v", err)
	}
	if p.StickyPartition() != 0 || p.AccumulatedBytes() != 6 || !p.IsAvailable(1) {
		t.Fatalf("state after rejects: sticky=%d acc=%d avail1=%v", p.StickyPartition(), p.AccumulatedBytes(), p.IsAvailable(1))
	}
	t.Logf("reject decision log:\n%s", stateLog.String())
}

func TestSinglePartition(t *testing.T) {
	// N=1：带键恒为 0；无键达 B 后也留在 0。
	ops := []op{
		{kind: "keyless", size: 3},
		{kind: "keyless", size: 100},
		{kind: "keyed", key: []byte("x")},
		{kind: "keyless", size: 2},
	}
	want := replayNaive(t, 1, 10, ops)
	got, p := replayReal(t, 1, 10, ops)
	assertResultsEqual(t, want, got)
	for i, r := range got {
		if r.err != nil || r.partition != 0 {
			t.Fatalf("N=1 op[%d] expected partition 0, got %+v", i, r)
		}
	}
	if p.StickyPartition() != 0 || p.AccumulatedBytes() != 2 {
		t.Fatalf("N=1 final state: sticky=%d acc=%d", p.StickyPartition(), p.AccumulatedBytes())
	}
}

func TestRandomDifferentialAgainstNaive(t *testing.T) {
	// 随机生成调用序列，要求生产实现与朴素模拟逐条一致；
	// 并验证同一序列重放两次得到完全相同的分区序列。
	keys := [][]byte{
		nil,
		{},
		[]byte("a"),
		[]byte("hello"),
		[]byte("ontology"),
		{0, 1, 2, 255},
	}
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(6)
		b := 1 + rng.Intn(15)
		length := 60 + rng.Intn(120)
		ops := make([]op, length)
		for i := range ops {
			switch rng.Intn(10) {
			case 0, 1, 2:
				ops[i] = op{kind: "keyed", key: keys[rng.Intn(len(keys))]}
			case 3, 4:
				ops[i] = op{
					kind:      "set",
					partition: rng.Intn(n+2) - 1, // 偶尔越界
					available: rng.Intn(2) == 0,
				}
			default:
				size := rng.Intn(b*3 + 2)
				if rng.Intn(20) == 0 {
					size = 0 // 偶尔触发 s<1 拒绝
				}
				ops[i] = op{kind: "keyless", size: size}
			}
		}

		want := replayNaive(t, n, b, ops)
		got1, p1 := replayReal(t, n, b, ops)
		got2, p2 := replayReal(t, n, b, ops)
		assertResultsEqual(t, want, got1)
		assertResultsEqual(t, got1, got2)
		if p1.StickyPartition() != p2.StickyPartition() || p1.AccumulatedBytes() != p2.AccumulatedBytes() {
			t.Fatalf("seed=%d replay state diverged", seed)
		}
		// 分区序列（忽略被拒绝的无键/设置调用）重放必须一致：
		// got1==got2 已在上面断言。
	}
}

func TestConcurrentCallsAreSafe(t *testing.T) {
	// 高并发混合调用：仅做 race 检测与状态合理性检查，
	// 串行等价性由互斥锁保证、差分测试验证行为本身。
	p, err := New(8, 64)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id) + 42))
			for i := 0; i < 2000; i++ {
				switch rng.Intn(6) {
				case 0:
					p.SendKeyed([]byte(fmt.Sprintf("key-%d", rng.Intn(50))))
				case 1:
					p.SetAvailable(rng.Intn(8), rng.Intn(2) == 0)
				case 2:
					if part, err := p.SendKeyless(1 + rng.Intn(100)); err == nil {
						if part < 0 || part >= 8 {
							t.Errorf("partition out of range: %d", part)
						}
					}
				case 3:
					sp := p.StickyPartition()
					if sp < 0 || sp >= 8 {
						t.Errorf("sticky out of range: %d", sp)
					}
				case 4:
					if acc := p.AccumulatedBytes(); acc < 0 || acc >= 64 {
						t.Errorf("accumulated out of expected range: %d", acc)
					}
				case 5:
					p.IsAvailable(rng.Intn(8))
				}
			}
		}(g)
	}
	wg.Wait()
}
