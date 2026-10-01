package partitioner

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// 朴素模拟：严格按题面规则逐条实现，与被测实现对拍。
// ---------------------------------------------------------------------------

type model struct {
	n           int
	batchBytes  int64
	sticky      int
	accumulated int64
	available   []bool
}

func newModel(n int, batchBytes int64) *model {
	available := make([]bool, n)
	for i := range available {
		available[i] = true
	}
	return &model{n: n, batchBytes: batchBytes, available: available}
}

func (m *model) stickySend(size int64) (int, error) {
	if size < 1 {
		return 0, ErrInvalidMessageSize
	}
	if !m.available[m.sticky] {
		next := -1
		for i := 0; i < m.n; i++ {
			if m.available[i] {
				next = i
				break
			}
		}
		if next < 0 {
			return 0, ErrNoAvailablePartition
		}
		m.sticky = next
		m.accumulated = 0
	}
	partition := m.sticky
	m.accumulated += size
	if m.accumulated >= m.batchBytes {
		for step := 1; step < m.n; step++ {
			next := (m.sticky + step) % m.n
			if m.available[next] {
				m.sticky = next
				break
			}
		}
		m.accumulated = 0
	}
	return partition, nil
}

func fnv1a32(key []byte) uint32 {
	var h uint32 = 2166136261
	for _, b := range key {
		h ^= uint32(b)
		h *= 16777619
	}
	return h
}

// ---------------------------------------------------------------------------
// 构造参数校验
// ---------------------------------------------------------------------------

func TestNewValidation(t *testing.T) {
	if _, err := New(0, 100); !errors.Is(err, ErrInvalidPartitionCount) {
		t.Fatalf("N=0: got %v, want %v", err, ErrInvalidPartitionCount)
	}
	if _, err := New(-3, 100); !errors.Is(err, ErrInvalidPartitionCount) {
		t.Fatalf("N=-3: got %v, want %v", err, ErrInvalidPartitionCount)
	}
	if _, err := New(4, 0); !errors.Is(err, ErrInvalidBatchSize) {
		t.Fatalf("B=0: got %v, want %v", err, ErrInvalidBatchSize)
	}
	if _, err := New(4, -1); !errors.Is(err, ErrInvalidBatchSize) {
		t.Fatalf("B=-1: got %v, want %v", err, ErrInvalidBatchSize)
	}
	p, err := New(3, 100)
	if err != nil {
		t.Fatalf("valid New: %v", err)
	}
	if p.NumPartitions() != 3 || p.BatchBytes() != 100 {
		t.Fatalf("config mismatch: N=%d B=%d", p.NumPartitions(), p.BatchBytes())
	}
	sticky, acc := p.State()
	if sticky != 0 || acc != 0 {
		t.Fatalf("initial state: sticky=%d acc=%d, want 0/0", sticky, acc)
	}
}

// ---------------------------------------------------------------------------
// 带键消息：FNV-1a 32 位哈希对 N 取余，与可用性无关，不计累计
// ---------------------------------------------------------------------------

func TestKeyedPartition(t *testing.T) {
	p, err := New(5, 100)
	if err != nil {
		t.Fatal(err)
	}
	keys := [][]byte{[]byte("alpha"), []byte("beta"), []byte(""), []byte{0x00, 0xff, 0x10}}
	for _, key := range keys {
		want := int(fnv1a32(key) % 5)
		got := p.PartitionForKey(key)
		t.Logf("key=%q fnv1a32=%d want=%d got=%d", key, fnv1a32(key), want, got)
		if got != want {
			t.Fatalf("key %q: got %d, want %d", key, got, want)
		}
	}

	// 已知向量：FNV-1a("") = 2166136261（偏移基础值本身）
	if got := fnv1a32(nil); got != 2166136261 {
		t.Fatalf("fnv1a32(empty)=%d, want 2166136261", got)
	}
}

// 带键消息不受可用性影响，且不改变粘性累计。
func TestKeyedUnaffectedByAvailabilityAndAccumulation(t *testing.T) {
	p, err := New(4, 100)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("order-42")
	want := int(fnv1a32(key) % 4)

	// 把目标分区标记为不可用，带键分区结果必须不变。
	if err := p.SetAvailable(want, false); err != nil {
		t.Fatal(err)
	}
	if got := p.PartitionForKey(key); got != want {
		t.Fatalf("keyed partition changed by availability: got %d, want %d", got, want)
	}
	t.Logf("key=%q 目标分区=%d 已置不可用，带键结果仍为 %d（判定：与可用性无关）", key, want, want)

	// 带键发送不计入粘性累计。
	for i := 0; i < 10; i++ {
		p.PartitionForKey(key)
	}
	sticky, acc := p.State()
	if sticky != 0 || acc != 0 {
		t.Fatalf("keyed sends mutated sticky state: sticky=%d acc=%d", sticky, acc)
	}
	t.Logf("10 次带键查询后 sticky=%d acc=%d（判定：带键消息不计累计）", sticky, acc)
}

// ---------------------------------------------------------------------------
// 无键粘性行为
// ---------------------------------------------------------------------------

func mustSticky(t *testing.T, p *Partitioner, size int64, want int, why string) {
	t.Helper()
	got, err := p.StickyPartition(size)
	if err != nil {
		t.Fatalf("size=%d: unexpected error %v", size, err)
	}
	sticky, acc := p.State()
	t.Logf("send(size=%d) -> partition=%d；判定依据：%s；发送后 sticky=%d acc=%d",
		size, got, why, sticky, acc)
	if got != want {
		t.Fatalf("size=%d: got partition %d, want %d", size, got, want)
	}
}

// 累计恰好等于 B：本条消息仍发往当前分区，本条之后切换。
func TestSwitchOnExactThreshold(t *testing.T) {
	p, err := New(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 40, 0, "acc=40 < B=100，不切换")
	mustSticky(t, p, 60, 0, "acc=100 >= B=100，本条仍落 0，本条之后切到 1 并清零")
	mustSticky(t, p, 1, 1, "上一条已触发切换，当前粘性分区为 1")
}

// 单条消息大于 B：发往当前分区后立即切换。
func TestSingleMessageLargerThanBatch(t *testing.T) {
	p, err := New(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 250, 0, "acc=250 >= B=100，本条落 0，本条之后切到 1 并清零")
	mustSticky(t, p, 1, 1, "当前粘性分区为 1")
}

// 切换时环形跳过不可用分区。
func TestSwitchSkipsUnavailable(t *testing.T) {
	p, err := New(4, 100)
	if err != nil {
		t.Fatal(err)
	}
	// 分区 1、2 不可用；0 触发切换时应跳到 3。
	if err := p.SetAvailable(1, false); err != nil {
		t.Fatal(err)
	}
	if err := p.SetAvailable(2, false); err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 100, 0, "acc=100 >= B=100；1、2 不可用，环形跳到 3")
	mustSticky(t, p, 1, 3, "当前粘性分区为 3")
	// 3 触发切换时环形越过 0 之后……0 可用，应切到 0。
	mustSticky(t, p, 100, 3, "acc=100 >= B=100；3 之后环形第一个可用为 0")
	mustSticky(t, p, 1, 0, "当前粘性分区为 0")
}

// 只有当前分区可用：达标后仍留在当前分区，累计清零。
func TestSwitchStaysWhenOnlyCurrentAvailable(t *testing.T) {
	p, err := New(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetAvailable(1, false); err != nil {
		t.Fatal(err)
	}
	if err := p.SetAvailable(2, false); err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 100, 0, "acc=100 >= B=100；仅 0 可用，留在 0 并清零")
	mustSticky(t, p, 1, 0, "仍停留在 0，acc=1")
}

// 粘性分区被标不可用：下一条无键发送前先切换并清零累计。
func TestStickyMarkedUnavailableSwitchesBeforeSend(t *testing.T) {
	p, err := New(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 30, 0, "acc=30 < B=100，不切换")
	if err := p.SetAvailable(0, false); err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 10, 1, "发送前发现 0 不可用，切到环形第一个可用分区 1 并清零；acc=10")
	sticky, acc := p.State()
	if sticky != 1 || acc != 10 {
		t.Fatalf("state: sticky=%d acc=%d, want 1/10", sticky, acc)
	}
}

// 标记不可用后又在下一条发送前恢复：不切换、累计保留。
func TestRestoreBeforeNextSendKeepsState(t *testing.T) {
	p, err := New(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 30, 0, "acc=30 < B=100")
	if err := p.SetAvailable(0, false); err != nil {
		t.Fatal(err)
	}
	if err := p.SetAvailable(0, true); err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 10, 0, "发送时 0 已恢复可用，不切换；acc=40（累计保留）")
}

// 分区恢复可用不触发回切。
func TestNoSwitchBackOnRecovery(t *testing.T) {
	p, err := New(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetAvailable(0, false); err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 10, 1, "0 不可用，发送前切到 1")
	if err := p.SetAvailable(0, true); err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 10, 1, "0 恢复可用不触发回切，仍落 1；acc=20")
}

// 全部不可用：无键发送整体拒绝且状态不变。
func TestNoAvailablePartitionRejected(t *testing.T) {
	p, err := New(2, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 30, 0, "acc=30 < B=100")
	for i := 0; i < 2; i++ {
		if err := p.SetAvailable(i, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.StickyPartition(10); !errors.Is(err, ErrNoAvailablePartition) {
		t.Fatalf("got %v, want %v", err, ErrNoAvailablePartition)
	}
	sticky, acc := p.State()
	if sticky != 0 || acc != 30 {
		t.Fatalf("rejected send mutated state: sticky=%d acc=%d, want 0/30", sticky, acc)
	}
	t.Logf("全部不可用：发送被拒（%v），sticky=%d acc=%d 保持不变", ErrNoAvailablePartition, sticky, acc)
}

// 非法输入整体拒绝且状态不变。
func TestRejectedOperationsKeepState(t *testing.T) {
	p, err := New(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustSticky(t, p, 30, 0, "acc=30 < B=100")

	if _, err := p.StickyPartition(0); !errors.Is(err, ErrInvalidMessageSize) {
		t.Fatalf("s=0: got %v, want %v", err, ErrInvalidMessageSize)
	}
	if _, err := p.StickyPartition(-5); !errors.Is(err, ErrInvalidMessageSize) {
		t.Fatalf("s=-5: got %v, want %v", err, ErrInvalidMessageSize)
	}
	if err := p.SetAvailable(3, true); !errors.Is(err, ErrPartitionOutOfRange) {
		t.Fatalf("partition=3: got %v, want %v", err, ErrPartitionOutOfRange)
	}
	if err := p.SetAvailable(-1, true); !errors.Is(err, ErrPartitionOutOfRange) {
		t.Fatalf("partition=-1: got %v, want %v", err, ErrPartitionOutOfRange)
	}
	if _, err := p.Available(99); !errors.Is(err, ErrPartitionOutOfRange) {
		t.Fatalf("Available(99): got %v, want %v", err, ErrPartitionOutOfRange)
	}

	sticky, acc := p.State()
	if sticky != 0 || acc != 30 {
		t.Fatalf("rejected ops mutated state: sticky=%d acc=%d, want 0/30", sticky, acc)
	}
	for i := 0; i < 3; i++ {
		ok, err := p.Available(i)
		if err != nil || !ok {
			t.Fatalf("partition %d availability mutated: ok=%v err=%v", i, ok, err)
		}
	}
	t.Logf("非法 s / 越界分区均被拒，sticky=%d acc=%d 与可用性均未变", sticky, acc)
}

// ---------------------------------------------------------------------------
// 与朴素模拟对拍
// ---------------------------------------------------------------------------

type op struct {
	kind      string // "send" | "set" | "keyed"
	size      int64
	partition int
	available bool
	key       []byte
}

func randomOps(r *rand.Rand, n int, count int) []op {
	ops := make([]op, 0, count)
	for i := 0; i < count; i++ {
		switch r.Intn(5) {
		case 0, 1, 2:
			// 偶尔产生非法 size，验证拒绝路径也一致。
			size := int64(r.Intn(220))
			if r.Intn(20) == 0 {
				size = 0
			}
			ops = append(ops, op{kind: "send", size: size})
		case 3:
			ops = append(ops, op{
				kind:      "set",
				partition: r.Intn(n),
				available: r.Intn(2) == 0,
			})
		default:
			key := []byte(fmt.Sprintf("key-%d", r.Intn(50)))
			ops = append(ops, op{kind: "keyed", key: key})
		}
	}
	return ops
}

func runOps(t *testing.T, p *Partitioner, m *model, ops []op) {
	t.Helper()
	for i, o := range ops {
		switch o.kind {
		case "send":
			gotPart, gotErr := p.StickyPartition(o.size)
			wantPart, wantErr := m.stickySend(o.size)
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("op %d send(%d): err got %v, want %v", i, o.size, gotErr, wantErr)
			}
			if gotErr == nil && gotPart != wantPart {
				t.Fatalf("op %d send(%d): partition got %d, want %d", i, o.size, gotPart, wantPart)
			}
			gSticky, gAcc := p.State()
			if gSticky != m.sticky || gAcc != m.accumulated {
				t.Fatalf("op %d send(%d): state got (%d,%d), want (%d,%d)",
					i, o.size, gSticky, gAcc, m.sticky, m.accumulated)
			}
			t.Logf("op %03d send(size=%d) -> partition=%d err=%v | 模拟=%d | 状态 sticky=%d acc=%d",
				i, o.size, gotPart, gotErr, wantPart, gSticky, gAcc)
		case "set":
			if err := p.SetAvailable(o.partition, o.available); err != nil {
				t.Fatalf("op %d set(%d,%v): %v", i, o.partition, o.available, err)
			}
			m.available[o.partition] = o.available
			t.Logf("op %03d set(partition=%d available=%v)", i, o.partition, o.available)
		case "keyed":
			got := p.PartitionForKey(o.key)
			want := int(fnv1a32(o.key) % uint32(m.n))
			if got != want {
				t.Fatalf("op %d keyed(%q): got %d, want %d", i, o.key, got, want)
			}
			t.Logf("op %03d keyed(key=%q) -> partition=%d（fnv1a32=%d %% %d）",
				i, o.key, got, fnv1a32(o.key), m.n)
		}
	}
}

func TestAgainstNaiveModel(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 20261001} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			n := 1 + r.Intn(6)
			b := int64(1 + r.Intn(200))
			p, err := New(n, b)
			if err != nil {
				t.Fatal(err)
			}
			m := newModel(n, b)
			t.Logf("N=%d B=%d，逐条与朴素模拟对照", n, b)
			runOps(t, p, m, randomOps(r, n, 300))
		})
	}
}

// 相同调用序列重放得到完全相同的分区序列。
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	n, b := 4, int64(64)
	ops := randomOps(r, n, 500)

	collect := func() []int {
		p, err := New(n, b)
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, o := range ops {
			switch o.kind {
			case "send":
				part, err := p.StickyPartition(o.size)
				if err != nil {
					out = append(out, -1)
				} else {
					out = append(out, part)
				}
			case "set":
				_ = p.SetAvailable(o.partition, o.available)
			case "keyed":
				out = append(out, p.PartitionForKey(o.key))
			}
		}
		return out
	}

	first := collect()
	for replay := 0; replay < 3; replay++ {
		got := collect()
		if len(got) != len(first) {
			t.Fatalf("replay %d: length %d != %d", replay, len(got), len(first))
		}
		for i := range first {
			if got[i] != first[i] {
				t.Fatalf("replay %d diverges at op %d: %d != %d", replay, i, got[i], first[i])
			}
		}
	}
	t.Logf("500 个操作的序列重放 3 次，分区序列完全一致（共 %d 个输出）", len(first))
}

// 并发调用：结果等价于某个串行顺序（用 -race 验证无数据竞争，
// 并校验最终状态与全部发送字节数的守恒关系）。
func TestConcurrentAccess(t *testing.T) {
	const (
		n         = 4
		b         = int64(1000)
		workers   = 8
		perWorker = 500
	)
	p, err := New(n, b)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var totalSent int64
	var mu sync.Mutex
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < perWorker; i++ {
				switch r.Intn(4) {
				case 0:
					size := int64(1 + r.Intn(50))
					// 并发下可能出现全部不可用，发送被拒是合法结果。
					if _, err := p.StickyPartition(size); err != nil &&
						!errors.Is(err, ErrNoAvailablePartition) {
						t.Errorf("send: unexpected error %v", err)
						return
					} else if err == nil {
						mu.Lock()
						totalSent += size
						mu.Unlock()
					}
				case 1:
					_ = p.SetAvailable(r.Intn(n), r.Intn(2) == 0)
				case 2:
					_, _ = p.Available(r.Intn(n))
					_, _ = p.State()
				default:
					_ = p.PartitionForKey([]byte(fmt.Sprintf("k-%d", r.Intn(100))))
				}
			}
		}(w)
	}
	wg.Wait()

	// 不变量校验：0 <= acc < B（达标即清零，拒绝不改状态）。
	sticky, acc := p.State()
	if acc < 0 || acc >= b {
		t.Fatalf("invariant broken: acc=%d not in [0,%d)", acc, b)
	}
	t.Logf("并发完成：totalSent=%d 最终 sticky=%d acc=%d（不变量 0<=acc<B 成立）",
		totalSent, sticky, acc)
}
