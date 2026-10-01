package frame

import (
	"math/big"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustAppend(t *testing.T, p *Partition, keys ...int64) {
	t.Helper()
	for _, k := range keys {
		if err := p.Append(k); err != nil {
			t.Fatalf("Append(%d) unexpected error: %v", k, err)
		}
	}
}

func intervalsEqual(a, b []Interval) bool {
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

// TestCurrentRowTies 验证 ROWS 与 RANGE 在 CURRENT ROW 起点下对前置并列行的差别。
func TestCurrentRowTies(t *testing.T) {
	// 行:        0   1   2   3   4
	// 键:        1   2   2   2   3
	// 并列组:    0   <--- 1 --->  2
	keys := []int64{1, 2, 2, 2, 3}

	t.Run("ROWS current row at i=3 excludes earlier peers", func(t *testing.T) {
		p, _ := New(16)
		mustAppend(t, p, keys...)
		got, err := p.Frame(3, Spec{
			Mode: Rows,
			From: Bound{Kind: CurrentRowBound},
			To:   Bound{Kind: UnboundedFollowing},
			Excl: ExcludeNone,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []Interval{{From: 3, To: 5}}
		if !intervalsEqual(got, want) {
			t.Fatalf("ROWS: got %v, want [{3 5}]", got)
		}
	})

	t.Run("RANGE current row at i=3 includes earlier peers", func(t *testing.T) {
		p, _ := New(16)
		mustAppend(t, p, keys...)
		got, err := p.Frame(3, Spec{
			Mode: Range,
			From: Bound{Kind: CurrentRowBound},
			To:   Bound{Kind: UnboundedFollowing},
			Excl: ExcludeNone,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []Interval{{From: 1, To: 5}}
		if !intervalsEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("GROUPS current row at i=3 includes whole peer group", func(t *testing.T) {
		p, _ := New(16)
		mustAppend(t, p, keys...)
		got, err := p.Frame(3, Spec{
			Mode: Groups,
			From: Bound{Kind: CurrentRowBound},
			To:   Bound{Kind: CurrentRowBound},
			Excl: ExcludeNone,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []Interval{{From: 1, To: 4}}
		if !intervalsEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

// TestFrameValidationErrors 按规定次序验证四种可区分错误（只报第一个）。
func TestFrameValidationErrors(t *testing.T) {
	p, _ := New(16)
	mustAppend(t, p, 1, 2, 3)

	valid := func() Spec {
		return Spec{
			Mode: Rows,
			From: Bound{Kind: UnboundedPreceding},
			To:   Bound{Kind: UnboundedFollowing},
			Excl: ExcludeNone,
		}
	}

	check := func(name string, mutate func(Spec) Spec, want error) {
		t.Helper()
		s := mutate(valid())
		if _, err := p.Frame(1, s); err != want {
			t.Errorf("%s: got %v, want %v", name, err, want)
		}
	}

	check("bad mode", func(s Spec) Spec { s.Mode = Mode(99); return s }, ErrInvalidSpec)
	check("bad from kind", func(s Spec) Spec { s.From.Kind = BoundKind(99); return s }, ErrInvalidSpec)
	check("bad to kind", func(s Spec) Spec { s.To.Kind = BoundKind(-1); return s }, ErrInvalidSpec)
	check("bad exclusion", func(s Spec) Spec { s.Excl = Exclusion(99); return s }, ErrInvalidSpec)

	check("from unbounded following", func(s Spec) Spec {
		s.From = Bound{Kind: UnboundedFollowing}
		s.To = Bound{Kind: UnboundedFollowing}
		return s
	}, ErrInvalidBoundPair)
	check("to unbounded preceding", func(s Spec) Spec {
		s.From = Bound{Kind: UnboundedPreceding}
		s.To = Bound{Kind: UnboundedPreceding}
		return s
	}, ErrInvalidBoundPair)
	check("start after end", func(s Spec) Spec {
		s.From = Bound{Kind: Following, N: 1}
		s.To = Bound{Kind: Preceding, N: 1}
		return s
	}, ErrInvalidBoundPair)

	check("negative from n", func(s Spec) Spec {
		s.From = Bound{Kind: Preceding, N: -1}
		s.To = Bound{Kind: Following, N: 0}
		return s
	}, ErrNegativeOffset)
	check("negative to n", func(s Spec) Spec {
		s.From = Bound{Kind: UnboundedPreceding}
		s.To = Bound{Kind: Following, N: -1}
		return s
	}, ErrNegativeOffset)
	check("pair error beats negative n", func(s Spec) Spec {
		s.From = Bound{Kind: Following, N: -1}
		s.To = Bound{Kind: Preceding, N: -1}
		return s
	}, ErrInvalidBoundPair)

	if _, err := p.Frame(3, valid()); err != ErrRowOutOfRange {
		t.Errorf("i=len: got %v, want %v", err, ErrRowOutOfRange)
	}
	if _, err := p.Frame(-1, valid()); err != ErrRowOutOfRange {
		t.Errorf("i=-1: got %v, want %v", err, ErrRowOutOfRange)
	}

	// 非偏移界上的 N 被忽略：负 N 也不报错。
	s := valid()
	s.From = Bound{Kind: UnboundedPreceding, N: -99}
	s.To = Bound{Kind: CurrentRowBound, N: -99}
	if _, err := p.Frame(1, s); err != nil {
		t.Errorf("N on non-offset bounds must be ignored, got %v", err)
	}
}

// TestAppendErrorsNoStateChange 验证追加被拒后分区状态不变。
func TestAppendErrorsNoStateChange(t *testing.T) {
	if _, err := New(0); err != ErrInvalidCapacity {
		t.Fatalf("New(0): got %v, want %v", err, ErrInvalidCapacity)
	}
	if _, err := New(-5); err != ErrInvalidCapacity {
		t.Fatalf("New(-5): got %v, want %v", err, ErrInvalidCapacity)
	}

	p, _ := New(2)
	if err := p.Append(5); err != nil {
		t.Fatal(err)
	}
	if err := p.Append(5); err != nil {
		t.Fatal(err)
	}
	// 容量已满优先于键乱序。
	if err := p.Append(1); err != ErrCapacityFull {
		t.Fatalf("full: got %v, want %v", err, ErrCapacityFull)
	}

	p2, _ := New(4)
	mustAppend(t, p2, 5)
	if err := p2.Append(4); err != ErrKeyOutOfOrder {
		t.Fatalf("out of order: got %v, want %v", err, ErrKeyOutOfOrder)
	}
	// 被拒绝后仍可追加合法键，且并列组统计正确。
	mustAppend(t, p2, 5, 6)
	got, err := p2.Frame(1, Spec{
		Mode: Groups,
		From: Bound{Kind: CurrentRowBound},
		To:   Bound{Kind: UnboundedFollowing},
		Excl: ExcludeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Interval{{From: 0, To: 3}} // 当前组 [5,5] 连同后续组 [6]
	if !intervalsEqual(got, want) {
		t.Fatalf("state changed after rejected append: got %v, want %v", got, want)
	}
}

// ---- 朴素参考实现与随机对拍 ----

// naiveFrame 严格按题目条件逐行判定，RANGE 使用 math/big 精确比较。
func naiveFrame(keys []int64, i int, spec Spec) []Interval {
	groupOf := make([]int, len(keys))
	groupCount := 0
	for j := range keys {
		if j > 0 && keys[j] != keys[j-1] {
			groupCount++
		}
		groupOf[j] = groupCount
	}

	keyI := big.NewInt(keys[i])
	diff := new(big.Int)

	numStart := func(b Bound, d *big.Int) bool {
		switch b.Kind {
		case UnboundedPreceding:
			return true
		case Preceding:
			return d.Cmp(new(big.Int).Neg(big.NewInt(b.N))) >= 0
		case CurrentRowBound:
			return d.Sign() >= 0
		case Following:
			return d.Cmp(big.NewInt(b.N)) >= 0
		case UnboundedFollowing:
			return true
		}
		return false
	}
	numEnd := func(b Bound, d *big.Int) bool {
		switch b.Kind {
		case UnboundedPreceding:
			return true
		case Preceding:
			return d.Cmp(new(big.Int).Neg(big.NewInt(b.N))) <= 0
		case CurrentRowBound:
			return d.Sign() <= 0
		case Following:
			return d.Cmp(big.NewInt(b.N)) <= 0
		case UnboundedFollowing:
			return true
		}
		return false
	}

	member := make([]bool, len(keys))
	for j := range keys {
		d := diff.SetInt64(keys[j]).Sub(diff, keyI)
		switch spec.Mode {
		case Rows:
			d.SetInt64(int64(j - i))
		case Groups:
			d.SetInt64(int64(groupOf[j] - groupOf[i]))
		}
		if !numStart(spec.From, d) || !numEnd(spec.To, d) {
			continue
		}
		switch spec.Excl {
		case ExcludeCurrentRow:
			if j == i {
				continue
			}
		case ExcludeGroup:
			if groupOf[j] == groupOf[i] {
				continue
			}
		case ExcludeTies:
			if groupOf[j] == groupOf[i] && j != i {
				continue
			}
		}
		member[j] = true
	}

	var out []Interval
	for j := 0; j < len(member); j++ {
		if !member[j] {
			continue
		}
		lo := j
		for j < len(member) && member[j] {
			j++
		}
		out = append(out, Interval{From: lo, To: j})
	}
	return out
}

func randomBound(r *rand.Rand, kinds []BoundKind, offsets []int64) Bound {
	k := kinds[r.Intn(len(kinds))]
	b := Bound{Kind: k}
	if k == Preceding || k == Following {
		b.N = offsets[r.Intn(len(offsets))]
	}
	return b
}

func randomSpec(r *rand.Rand) Spec {
	allKinds := []BoundKind{UnboundedPreceding, Preceding, CurrentRowBound, Following, UnboundedFollowing}
	offsets := []int64{0, 1, 2, 3, 5, 100, 1 << 40, 1 << 62, -1, -1 << 62}
	mode := Mode(r.Intn(3))
	excl := Exclusion(r.Intn(4))
	from := randomBound(r, allKinds, offsets)
	to := randomBound(r, allKinds, offsets)
	if r.Intn(3) != 0 {
		fk := allKinds[r.Intn(len(allKinds))]
		tk := allKinds[r.Intn(len(allKinds))]
		if fk > tk {
			fk, tk = tk, fk
		}
		if fk == UnboundedFollowing {
			fk, tk = UnboundedFollowing, UnboundedFollowing
		}
		if tk == UnboundedPreceding {
			fk, tk = UnboundedPreceding, UnboundedPreceding
		}
		from = randomBound(r, []BoundKind{fk}, offsets)
		to = randomBound(r, []BoundKind{tk}, offsets)
	}
	return Spec{Mode: mode, From: from, To: to, Excl: excl}
}

func randomPartitionKeys(r *rand.Rand) []int64 {
	n := r.Intn(12)
	keys := make([]int64, n)
	anchored := r.Intn(4) == 0
	var key int64
	if anchored {
		switch r.Intn(3) {
		case 0:
			key = int64(-1 << 63)
		case 1:
			key = int64(1<<63 - 1)
		default:
			key = -3
		}
	} else {
		key = r.Int63n(21) - 10
	}
	for j := range keys {
		if !anchored {
			switch r.Intn(3) {
			case 0:
			case 1:
				key += r.Int63n(4)
			default:
				key += r.Int63n(3) + 1
			}
		}
		keys[j] = key
	}
	return keys
}

// TestRandomAgainstNaive 对拍 2000 组随机分区与 spec，日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(20261001))
	const total = 2000
	for c := 0; c < total; c++ {
		keys := randomPartitionKeys(r)
		p, _ := New(len(keys) + 1)
		for _, k := range keys {
			if err := p.Append(k); err != nil {
				t.Fatalf("case %d Append(%d): %v", c, k, err)
			}
		}
		spec := randomSpec(r)
		i := r.Intn(len(keys)+2) - 1 // 允许越界

		got, err := p.Frame(i, spec)
		basis := "d per mode (ROWS j-i / RANGE keyj-keyi big.Int / GROUPS gj-gi); start and end bound comparisons; exclusion; merge adjacent rows"
		t.Logf("CASE %d keys=%v i=%d spec=%q -> intervals=%v err=%v | basis: %s",
			c, keys, i, spec.String(), got, err, basis)

		expectErr := validateSpec(spec) != nil || i < 0 || i >= len(keys)
		if expectErr {
			if err == nil {
				t.Fatalf("case %d: expected error for keys=%v i=%d spec=%s", c, keys, i, spec)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: unexpected error %v for keys=%v i=%d spec=%s", c, err, keys, i, spec)
		}

		want := naiveFrame(keys, i, spec)
		if !intervalsEqual(got, want) {
			t.Fatalf("case %d MISMATCH\nkeys=%v i=%d spec=%s\ngot  %v\nwant %v", c, keys, i, spec, got, want)
		}
		assertIntervalInvariants(t, got, len(keys))
	}
}

func assertIntervalInvariants(t *testing.T, ivs []Interval, n int) {
	t.Helper()
	for k, iv := range ivs {
		if iv.From < 0 || iv.To > n || iv.From >= iv.To {
			t.Fatalf("bad interval %v (n=%d)", iv, n)
		}
		if k > 0 && iv.From <= ivs[k-1].To {
			t.Fatalf("adjacent/overlapping intervals: %v then %v", ivs[k-1], iv)
		}
	}
}

// TestDeterministicReplay 验证相同操作序列重放得到完全相同的结果。
func TestDeterministicReplay(t *testing.T) {
	r := rand.New(rand.NewSource(4242))
	keys := randomPartitionKeys(r)
	type query struct {
		i    int
		spec Spec
	}
	var queries []query
	for range 50 {
		queries = append(queries, query{i: r.Intn(len(keys)+2) - 1, spec: randomSpec(r)})
	}

	type result struct {
		ivs []Interval
		err error
	}
	run := func() []result {
		p, _ := New(len(keys) + 1)
		var out []result
		for _, k := range keys {
			if err := p.Append(k); err != nil {
				t.Fatalf("Append(%d): %v", k, err)
			}
		}
		for _, q := range queries {
			ivs, err := p.Frame(q.i, q.spec)
			out = append(out, result{ivs: ivs, err: err})
		}
		return out
	}

	first := run()
	for k := 0; k < 3; k++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("replay %d differs", k)
		}
	}
}

// TestReturnedSliceNotShared 验证修改返回切片不影响内部状态与后续结果。
func TestReturnedSliceNotShared(t *testing.T) {
	p, _ := New(8)
	mustAppend(t, p, 1, 2, 3)
	full := Spec{
		Mode: Rows,
		From: Bound{Kind: UnboundedPreceding},
		To:   Bound{Kind: UnboundedFollowing},
		Excl: ExcludeNone,
	}
	first, err := p.Frame(1, full)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = Interval{From: 999, To: 1000}
	second, err := p.Frame(1, full)
	if err != nil {
		t.Fatal(err)
	}
	want := []Interval{{From: 0, To: 3}}
	if !intervalsEqual(second, want) {
		t.Fatalf("internal state leaked through returned slice: got %v", second)
	}
}

// TestConcurrentAppendAndFrame 在 -race 下验证并发安全与可串行化。
func TestConcurrentAppendAndFrame(t *testing.T) {
	const workers = 8
	const perWorker = 200
	p, _ := New(workers * perWorker)
	var wg sync.WaitGroup
	full := Spec{
		Mode: Range,
		From: Bound{Kind: UnboundedPreceding},
		To:   Bound{Kind: UnboundedFollowing},
		Excl: ExcludeGroup,
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < perWorker; k++ {
				if err := p.Append(7); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				if n := p.Len(); n > 0 {
					ivs, err := p.Frame(k%n, full)
					if err != nil {
						t.Errorf("Frame: %v", err)
						return
					}
					for _, iv := range ivs {
						if iv.From < 0 || iv.To > n || iv.From >= iv.To {
							t.Errorf("bad interval %v n=%d", iv, n)
							return
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	if got, want := p.Len(), workers*perWorker; got != want {
		t.Fatalf("Len: got %d, want %d", got, want)
	}
}

// TestRangePrecedingBoundary 验证 RANGE n PRECEDING 恰含键等于 key-n 的行。
func TestRangePrecedingBoundary(t *testing.T) {
	// 键: 0 2 5 5 8，i=3(key=5), n=5 -> 恰好 key=0 的行 0
	p, _ := New(16)
	mustAppend(t, p, 0, 2, 5, 5, 8)
	got, err := p.Frame(3, Spec{
		Mode: Range,
		From: Bound{Kind: Preceding, N: 5},
		To:   Bound{Kind: Preceding, N: 5},
		Excl: ExcludeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Interval{{From: 0, To: 1}}
	if !intervalsEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestGroupsNeighbors 验证 GROUPS 1 PRECEDING 到 1 FOLLOWING 跨并列组。
func TestGroupsNeighbors(t *testing.T) {
	// 键: 1 2 2 3 3 3 4，组: 0 | 1 | 2 | 3；i=3 在组 2。
	p, _ := New(16)
	mustAppend(t, p, 1, 2, 2, 3, 3, 3, 4)
	got, err := p.Frame(3, Spec{
		Mode: Groups,
		From: Bound{Kind: Preceding, N: 1},
		To:   Bound{Kind: Following, N: 1},
		Excl: ExcludeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Interval{{From: 1, To: 7}} // 组 1、2、3
	if !intervalsEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestEmptyFrameBothPreceding 验证 3 PRECEDING 到 5 PRECEDING 的空帧。
func TestEmptyFrameBothPreceding(t *testing.T) {
	// d >= -3 且 d <= -5 无交集（ROWS 下各模式同理）。
	for _, mode := range []Mode{Rows, Range, Groups} {
		p, _ := New(16)
		mustAppend(t, p, 1, 2, 3, 4, 5, 6, 7)
		got, err := p.Frame(6, Spec{
			Mode: mode,
			From: Bound{Kind: Preceding, N: 3},
			To:   Bound{Kind: Preceding, N: 5},
			Excl: ExcludeNone,
		})
		if err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
		if len(got) != 0 {
			t.Fatalf("mode %v: got %v, want empty", mode, got)
		}
	}
}

// TestRangeInt64Extremes 验证键差超出 int64 时比较结论仍精确。
func TestRangeInt64Extremes(t *testing.T) {
	const minInt = int64(-1 << 63)
	const maxInt = int64(1<<63 - 1)

	// i=1 (maxInt), n FOLLOWING=0：minInt 行差为 -2^64-1 < 0，必须被排除；
	// 若按 int64 回绕，差值会变成 1 从而错误纳入。
	p, _ := New(8)
	mustAppend(t, p, minInt, maxInt)
	got, err := p.Frame(1, Spec{
		Mode: Range,
		From: Bound{Kind: CurrentRowBound},
		To:   Bound{Kind: CurrentRowBound},
		Excl: ExcludeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Interval{{From: 1, To: 2}}
	if !intervalsEqual(got, want) {
		t.Fatalf("got %v, want %v (wrap-around would wrongly include row 0)", got, want)
	}

	// i=0 (minInt)，1 PRECEDING：maxInt 行差为 2^64-1，-1 比较应排除；
	// 回绕会得到 -1 从而错误纳入（且还会错误满足 d >= -1）。
	p2, _ := New(8)
	mustAppend(t, p2, minInt, maxInt)
	got2, err := p2.Frame(0, Spec{
		Mode: Range,
		From: Bound{Kind: Preceding, N: 1},
		To:   Bound{Kind: Preceding, N: 1},
		Excl: ExcludeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Fatalf("got %v, want empty", got2)
	}

	// RANGE 1 FOLLOWING 在 maxInt 当前行上：阈值溢出但无后续键，帧为自身。
	p3, _ := New(8)
	mustAppend(t, p3, maxInt, maxInt)
	got3, err := p3.Frame(1, Spec{
		Mode: Range,
		From: Bound{Kind: Following, N: 1},
		To:   Bound{Kind: Following, N: 1},
		Excl: ExcludeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got3) != 0 {
		t.Fatalf("got %v, want empty", got3)
	}
}

// TestExclusions 验证 TIES 保留自身、GROUP 连自身去掉、CURRENT ROW 使帧分裂。
func TestExclusions(t *testing.T) {
	// 键: 1 2 2 2 3，i=2，帧 RANGE CURRENT ROW..UNBOUNDED FOLLOWING = 行 1..4。
	newP := func() *Partition {
		p, _ := New(16)
		mustAppend(t, p, 1, 2, 2, 2, 3)
		return p
	}
	spec := func(e Exclusion) Spec {
		return Spec{
			Mode: Range,
			From: Bound{Kind: CurrentRowBound},
			To:   Bound{Kind: UnboundedFollowing},
			Excl: e,
		}
	}

	t.Run("EXCLUDE TIES keeps self drops peer rows", func(t *testing.T) {
		got, err := newP().Frame(2, spec(ExcludeTies))
		if err != nil {
			t.Fatal(err)
		}
		want := []Interval{{From: 2, To: 3}, {From: 4, To: 5}}
		if !intervalsEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("EXCLUDE GROUP drops self as well", func(t *testing.T) {
		got, err := newP().Frame(2, spec(ExcludeGroup))
		if err != nil {
			t.Fatal(err)
		}
		want := []Interval{{From: 4, To: 5}}
		if !intervalsEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("EXCLUDE CURRENT ROW splits into two pieces", func(t *testing.T) {
		// 帧 ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING 去掉行 2。
		p, _ := New(16)
		mustAppend(t, p, 1, 2, 2, 2, 3)
		got, err := p.Frame(2, Spec{
			Mode: Rows,
			From: Bound{Kind: UnboundedPreceding},
			To:   Bound{Kind: UnboundedFollowing},
			Excl: ExcludeCurrentRow,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []Interval{{From: 0, To: 2}, {From: 3, To: 5}}
		if !intervalsEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("EXCLUDE TIES does not add self when outside frame", func(t *testing.T) {
		// i=2，ROWS 3 FOLLOWING..UNBOUNDED FOLLOWING：自身不在帧内，排除不补入。
		got, err := newP().Frame(2, Spec{
			Mode: Rows,
			From: Bound{Kind: Following, N: 1},
			To:   Bound{Kind: UnboundedFollowing},
			Excl: ExcludeTies,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []Interval{{From: 4, To: 5}}
		if !intervalsEqual(got, want) {
			t.Fatalf("EXCLUDE TIES: got %v, want [{4 5}]", got)
		}
	})
}
