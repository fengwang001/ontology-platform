package counter

import (
	"errors"
	"math/big"
	"sync"
	"testing"
)

func valuesOf(t *testing.T, c *Counter) []*big.Int {
	t.Helper()
	vs := make([]*big.Int, c.ReplicaCount())
	for i := range vs {
		v, err := c.Value(i)
		if err != nil {
			t.Fatalf("Value(%d) 意外失败: %v", i, err)
		}
		vs[i] = v
	}
	return vs
}

func logValues(t *testing.T, c *Counter, reason string) {
	t.Helper()
	t.Logf("  判定依据=%s；各副本值=%v", reason, valuesOf(t, c))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustValue(c *Counter, id int) *big.Int {
	v, err := c.Value(id)
	if err != nil {
		panic(err)
	}
	return v
}

func uint64sEqual(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func snapshotsEqual(a, b []ReplicaState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !uint64sEqual(a[i].P, b[i].P) || !uint64sEqual(a[i].N, b[i].N) {
			return false
		}
	}
	return true
}

// TestNewErrors 校验构造参数非法时返回 ErrInvalidArgument。
func TestNewErrors(t *testing.T) {
	cases := []struct {
		n     int
		limit uint64
	}{
		{0, 10},
		{-1, 10},
		{3, 0},
	}
	for _, tc := range cases {
		_, err := New(tc.n, tc.limit)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%d,%d) err=%v, 期望 ErrInvalidArgument", tc.n, tc.limit, err)
		}
		t.Logf("输入 New(n=%d, limit=%d) -> 拒绝，原因 ErrInvalidArgument", tc.n, tc.limit)
	}
}

// TestInvalidInputs 覆盖四类互不相同的错误，并验证失败不留痕。
func TestInvalidInputs(t *testing.T) {
	const limit = uint64(10)
	c, err := New(3, limit)
	if err != nil {
		t.Fatal(err)
	}
	must(t, c.Increment(0, 4))
	must(t, c.Decrement(1, 2))
	before := c.SnapshotAll()

	check := func(name string, call func() error, want error) {
		t.Helper()
		err := call()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err=%v, 期望 %v", name, err, want)
		}
		if after := c.SnapshotAll(); !snapshotsEqual(before, after) {
			t.Fatalf("%s: 被拒操作改变了状态: before=%v after=%v", name, before, after)
		}
		t.Logf("输入 %s -> 拒绝(%v)；快照不变，失败不留痕", name, want)
	}

	check("Increment 不存在副本(3)", func() error { return c.Increment(3, 1) }, ErrUnknownReplica)
	check("Increment 负副本(-1)", func() error { return c.Increment(-1, 1) }, ErrUnknownReplica)
	check("Decrement 不存在副本(9)", func() error { return c.Decrement(9, 1) }, ErrUnknownReplica)
	check("Merge dst 不存在", func() error { return c.Merge(3, 0) }, ErrUnknownReplica)
	check("Merge src 不存在", func() error { return c.Merge(0, 3) }, ErrUnknownReplica)
	check("Value 不存在副本", func() error { _, e := c.Value(3); return e }, ErrUnknownReplica)
	check("Snapshot 不存在副本", func() error { _, e := c.Snapshot(3); return e }, ErrUnknownReplica)

	check("Increment 零增量", func() error { return c.Increment(0, 0) }, ErrNonPositiveDelta)
	check("Decrement 零增量", func() error { return c.Decrement(0, 0) }, ErrNonPositiveDelta)

	// 当前分量 4，再 +7 -> 11 > 10；减量分量 2，再 +9 -> 11 > 10。
	check("Increment 超上限", func() error { return c.Increment(0, limit-4+1) }, ErrComponentOverflow)
	check("Decrement 超上限", func() error { return c.Decrement(1, limit-2+1) }, ErrComponentOverflow)

	errs := []error{ErrInvalidArgument, ErrUnknownReplica, ErrNonPositiveDelta, ErrComponentOverflow}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("错误类别不可区分: %v 与 %v", errs[i], errs[j])
			}
		}
	}
	logValues(t, c, "全部非法输入被整体拒绝后状态不变")
}

// TestNegativeAllowed 值允许为负，不因变负被拒绝或截断，且负值随合并传播。
func TestNegativeAllowed(t *testing.T) {
	c, _ := New(2, 100)
	must(t, c.Increment(0, 5))
	must(t, c.Decrement(0, 8))
	if v := mustValue(c, 0); v.Cmp(big.NewInt(-3)) != 0 {
		t.Fatalf("值应为 -3，实际 %s", v)
	}
	must(t, c.Merge(1, 0))
	if v := mustValue(c, 1); v.Sign() >= 0 {
		t.Fatalf("负值应随合并传播，实际 %s", v)
	}
	t.Logf("输入 增5减8 -> 副本0=%s，Merge(1,0) 后副本1=%s（允许为负）",
		mustValue(c, 0), mustValue(c, 1))
}

// build3 构造：副本0增6；副本1增4减9；副本2减3。
func build3(t *testing.T) *Counter {
	t.Helper()
	c, err := New(3, 1000)
	if err != nil {
		t.Fatal(err)
	}
	must(t, c.Increment(0, 6))
	must(t, c.Increment(1, 4))
	must(t, c.Decrement(1, 9))
	must(t, c.Decrement(2, 3))
	return c
}

// TestMergeIdempotent 幂等：重复合并不变；同副本合并为合法空操作。
func TestMergeIdempotent(t *testing.T) {
	c := build3(t)
	must(t, c.Merge(0, 1))
	once := c.SnapshotAll()
	for k := 0; k < 5; k++ {
		must(t, c.Merge(0, 1))
	}
	if after := c.SnapshotAll(); !snapshotsEqual(once, after) {
		t.Fatalf("重复合并不应改变状态: once=%v after=%v", once, after)
	}
	must(t, c.Merge(2, 2))
	t.Logf("Merge(0,1) 后再合并 5 次快照一致；Merge(2,2) 为合法空操作，值=%v", valuesOf(t, c))
}

// TestMergeCommutative 交换律：互逆合并顺序不同，最终状态一致。
func TestMergeCommutative(t *testing.T) {
	orderA := build3(t)
	orderB := build3(t)
	must(t, orderA.Merge(0, 1))
	must(t, orderA.Merge(1, 0))
	must(t, orderB.Merge(1, 0))
	must(t, orderB.Merge(0, 1))
	if !snapshotsEqual(orderA.SnapshotAll(), orderB.SnapshotAll()) {
		t.Fatal("合并交换律不成立")
	}
	t.Logf("两种互逆合并顺序后快照一致，交换律成立：%v", valuesOf(t, orderA))
}

// TestMergeAssociative 结合律：(0<-1)<-2 与 0<-(1<-2) 在副本0上一致。
func TestMergeAssociative(t *testing.T) {
	left := build3(t)
	right := build3(t)
	must(t, left.Merge(0, 1))
	must(t, left.Merge(0, 2))
	must(t, right.Merge(1, 2))
	must(t, right.Merge(0, 1))
	l0, _ := left.Snapshot(0)
	r0, _ := right.Snapshot(0)
	if !uint64sEqual(l0.P, r0.P) || !uint64sEqual(l0.N, r0.N) {
		t.Fatalf("合并结合律不成立: %v vs %v", l0, r0)
	}
	want := big.NewInt(6 + 4 - 9 - 3)
	if mustValue(left, 0).Cmp(want) != 0 || mustValue(right, 0).Cmp(want) != 0 {
		t.Fatalf("两种结合方式副本0应收敛到 %s", want)
	}
	t.Logf("两种结合方式副本0均为 %s，结合律成立", want)
}

// TestConvergenceAgainstNaive 乱序、重复双向合并后与朴素参照逐分量、逐值核对。
func TestConvergenceAgainstNaive(t *testing.T) {
	const n = 4
	c, _ := New(n, 500)

	// 朴素参照：本地操作只改自身分量且单调不减，全量传播后
	// 每个副本的第 j 个分量应等于副本 j 的本地累计增/减。
	refP := make([]uint64, n)
	refN := make([]uint64, n)
	deltas := []struct {
		rep        int
		inc, decBy uint64
	}{
		{0, 10, 3},
		{1, 25, 7},
		{2, 2, 40},
		{3, 100, 1},
	}
	for _, d := range deltas {
		must(t, c.Increment(d.rep, d.inc))
		must(t, c.Decrement(d.rep, d.decBy))
		refP[d.rep] += d.inc
		refN[d.rep] += d.decBy
		t.Logf("输入 副本%d 增%d 减%d", d.rep, d.inc, d.decBy)
	}

	edges := [][2]int{{0, 1}, {2, 1}, {3, 0}, {1, 3}, {0, 2}, {2, 3}, {1, 0}, {3, 2}}
	for round := 0; round < 3; round++ {
		for _, e := range edges {
			if round%2 == 0 {
				must(t, c.Merge(e[0], e[1]))
			} else {
				must(t, c.Merge(e[1], e[0]))
			}
		}
	}

	ref := new(big.Int)
	for i := 0; i < n; i++ {
		ref.Add(ref, new(big.Int).SetUint64(refP[i]))
		ref.Sub(ref, new(big.Int).SetUint64(refN[i]))
	}
	for i := 0; i < n; i++ {
		if got := mustValue(c, i); got.Cmp(ref) != 0 {
			t.Fatalf("副本%d 值 %s 与朴素参照 %s 不一致", i, got, ref)
		}
		s, _ := c.Snapshot(i)
		for j := 0; j < n; j++ {
			if s.P[j] != refP[j] || s.N[j] != refN[j] {
				t.Fatalf("副本%d 分量%d 未收敛: got(%d,%d) want(%d,%d)",
					i, j, s.P[j], s.N[j], refP[j], refN[j])
			}
		}
	}
	must(t, c.SelfCheck())
	t.Logf("乱序/重复双向合并后 %d 个副本逐分量与参照一致，共同值=%s", n, ref)
	logValues(t, c, "全部副本相等且等于 sum(refP)-sum(refN)")
}

// TestReproducible 可复现：相同的操作序列（含合并顺序）必然得到相同结果。
func TestReproducible(t *testing.T) {
	run := func() *big.Int {
		c := build3(t)
		must(t, c.Merge(2, 0))
		must(t, c.Merge(0, 1))
		must(t, c.Merge(2, 1))
		must(t, c.Merge(1, 0))
		must(t, c.Merge(1, 2))
		must(t, c.Merge(0, 2))
		return mustValue(c, 0)
	}
	first, second := run(), run()
	if first.Cmp(second) != 0 {
		t.Fatalf("相同操作序列结果不可复现: %s vs %s", first, second)
	}
	t.Logf("两次执行相同操作序列，副本0均为 %s，可复现", first)
}

// TestConcurrentBidirectionalMerge 并发压力：增减、合并（互逆方向同时进行）、
// 求值、快照、自检交错执行；-race 下不得数据竞争，且不会死锁。
func TestConcurrentBidirectionalMerge(t *testing.T) {
	const n = 5
	c, _ := New(n, 100000)
	var wg sync.WaitGroup

	// 每个副本一个增减 worker，反复增与减（控制在上限内）。
	for r := 0; r < n; r++ {
		wg.Add(1)
		go func(rep int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				_ = c.Increment(rep, uint64(1+k%5))
				_ = c.Decrement(rep, uint64(1+(k%3)))
			}
		}(r)
	}

	// 合并 worker：互逆方向的合并在不同 goroutine 中同时进行。
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			wg.Add(2)
			go func(x, y int) {
				defer wg.Done()
				for k := 0; k < 200; k++ {
					_ = c.Merge(x, y)
				}
			}(a, b)
			go func(x, y int) {
				defer wg.Done()
				for k := 0; k < 200; k++ {
					_ = c.Merge(y, x)
				}
			}(a, b)
		}
	}

	// 只读 worker：求值、快照、自检与非法输入探测并发穿插。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				_, _ = c.Value(k % n)
				_, _ = c.Snapshot((k + 1) % n)
				_ = c.SelfCheck()
				_ = c.Merge(0, 0)
				if err := c.Increment(n, 1); !errors.Is(err, ErrUnknownReplica) {
					t.Errorf("并发下非法副本应返回 ErrUnknownReplica，得到 %v", err)
				}
			}
		}(w)
	}

	wg.Wait()

	// 并发结束后再做一轮全量双向同步，所有副本必须收敛到同一值。
	for round := 0; round < 2; round++ {
		for a := 0; a < n; a++ {
			for b := 0; b < n; b++ {
				must(t, c.Merge(a, b))
			}
		}
	}
	want := mustValue(c, 0)
	for i := 1; i < n; i++ {
		if got := mustValue(c, i); got.Cmp(want) != 0 {
			t.Fatalf("收敛失败：副本0=%s 副本%d=%s", want, i, got)
		}
	}
	must(t, c.SelfCheck())
	t.Logf("并发增减/双向合并/只读交错后全部收敛到共同值 %s", want)
}
