package sparsebitset

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// mustStats 断言容器表示统计，并打印判定依据。
func mustStats(t *testing.T, s *Set, wantArrays, wantBitmaps int, why string) {
	t.Helper()
	arrays, bitmaps := s.Stats()
	t.Logf("Stats: arrays=%d bitmaps=%d, 期望 arrays=%d bitmaps=%d, 依据: %s",
		arrays, bitmaps, wantArrays, wantBitmaps, why)
	if arrays != wantArrays || bitmaps != wantBitmaps {
		t.Fatalf("Stats = (%d, %d), 期望 (%d, %d): %s",
			arrays, bitmaps, wantArrays, wantBitmaps, why)
	}
}

// fill 向集合加入 [0, n) 中键为 key 的 n 个元素。
func fillContainer(s *Set, key uint32, n int) {
	for i := 0; i < n; i++ {
		s.Add(key<<16 | uint32(i))
	}
}

// TestThresholdAddDirection 覆盖 Add 方向上 4095/4096/4097 的表示转换。
func TestThresholdAddDirection(t *testing.T) {
	s := New()
	fillContainer(s, 0, 4095)
	mustStats(t, s, 1, 0, "基数 4095 <= 4096，必须是数组")

	s.Add(4095) // 第 4096 个元素
	mustStats(t, s, 1, 0, "基数恰为 4096，必须是数组")

	s.Add(4096) // 第 4097 个元素触发转换
	mustStats(t, s, 0, 1, "基数 4097 > 4096，Add 使数组转为位图")

	if got := s.Cardinality(); got != 4097 {
		t.Fatalf("Cardinality = %d, 期望 4097", got)
	}
	for i := uint32(0); i < 4097; i++ {
		if !s.Contains(i) {
			t.Fatalf("转换后丢失元素 %d", i)
		}
	}
}

// TestThresholdRemoveDirection 覆盖 Remove 方向上 4097/4096/4095 的表示转换。
func TestThresholdRemoveDirection(t *testing.T) {
	s := New()
	fillContainer(s, 0, 4097)
	mustStats(t, s, 0, 1, "基数 4097 > 4096，必须是位图")

	s.Remove(4096) // 降到 4096，触发回转
	mustStats(t, s, 1, 0, "Remove 使基数降到 4096，位图必须转回数组")

	s.Remove(4095) // 4095
	mustStats(t, s, 1, 0, "基数 4095，仍是数组")

	if got := s.Cardinality(); got != 4095 {
		t.Fatalf("Cardinality = %d, 期望 4095", got)
	}
	for i := uint32(0); i < 4095; i++ {
		if !s.Contains(i) {
			t.Fatalf("转换后丢失元素 %d", i)
		}
	}
}

// TestContainerRemovedWhenEmpty 覆盖容器清空后从集合中消失。
func TestContainerRemovedWhenEmpty(t *testing.T) {
	s := New()
	s.Add(1<<16 | 7)
	s.Add(3<<16 | 9)
	mustStats(t, s, 2, 0, "两个非空容器")

	s.Remove(1<<16 | 7)
	mustStats(t, s, 1, 0, "基数为 0 的容器必须被移除")

	s.Remove(3<<16 | 9)
	mustStats(t, s, 0, 0, "全部清空后集合无容器")
	if got := s.Cardinality(); got != 0 {
		t.Fatalf("Cardinality = %d, 期望 0", got)
	}
	// 删除不存在的元素是合法空操作。
	s.Remove(12345)
	s.Remove(1 << 20)
	mustStats(t, s, 0, 0, "Remove 不存在元素为空操作")
}

// TestAddRangeAcrossContainers 覆盖 AddRange 跨多个容器。
func TestAddRangeAcrossContainers(t *testing.T) {
	s := New()
	// 跨 3 个完整容器加边界余量：[65534, 3*65536+2)
	lo, hi := uint64(65534), uint64(3*65536+2)
	if err := s.AddRange(lo, hi); err != nil {
		t.Fatalf("AddRange 返回错误: %v", err)
	}
	wantCard := int(hi - lo)
	if got := s.Cardinality(); got != wantCard {
		t.Fatalf("Cardinality = %d, 期望 %d", got, wantCard)
	}
	// 容器 0: 2 个元素（数组）；容器 1、2: 各 65536（位图）；容器 3: 2 个元素（数组）。
	mustStats(t, s, 2, 2, "跨容器区间：两端余量为数组，中间满容器为位图")
	for _, x := range []uint32{65534, 65535, 65536, 131072, 196607, 196608, 196609} {
		if !s.Contains(x) {
			t.Fatalf("AddRange 后缺少元素 %d", x)
		}
	}
	if s.Contains(65533) || s.Contains(196610) {
		t.Fatal("AddRange 越出了半开区间边界")
	}
}

// TestAddRangeExactlyThreshold 覆盖 AddRange 恰好落在 4096 边界两侧。
func TestAddRangeExactlyThreshold(t *testing.T) {
	s := New()
	if err := s.AddRange(100, 100+4096); err != nil {
		t.Fatal(err)
	}
	mustStats(t, s, 1, 0, "AddRange 恰好 4096 个元素，必须是数组")

	if err := s.AddRange(100, 100+4097); err != nil {
		t.Fatal(err)
	}
	mustStats(t, s, 0, 1, "AddRange 达到 4097 个元素，必须转为位图")

	// 区间跨容器键边界且每侧恰为 2048（合计 4096 但分属两容器）。
	s2 := New()
	mid := uint64(1) << 16
	if err := s2.AddRange(mid-2048, mid+2048); err != nil {
		t.Fatal(err)
	}
	mustStats(t, s2, 2, 0, "跨键边界两侧各 2048，两个数组容器")
	if got := s2.Cardinality(); got != 4096 {
		t.Fatalf("Cardinality = %d, 期望 4096", got)
	}
}

// TestAddRangeFullUniverse 覆盖 hi 取到 2^32 的满域区间。
func TestAddRangeFullUniverse(t *testing.T) {
	s := New()
	if err := s.AddRange(0, 1<<32); err != nil {
		t.Fatalf("AddRange(0, 2^32) 返回错误: %v", err)
	}
	arrays, bitmaps := s.Stats()
	if arrays != 0 || bitmaps != 65536 {
		t.Fatalf("Stats = (%d, %d), 期望 (0, 65536)", arrays, bitmaps)
	}
	if got := s.Cardinality(); got != 1<<32 {
		t.Fatalf("Cardinality = %d, 期望 2^32", got)
	}
	if got := s.Rank(1<<32 - 1); got != 1<<32 {
		t.Fatalf("Rank(2^32-1) = %d, 期望 2^32", got)
	}
	v, err := s.Select(1<<32 - 1)
	if err != nil || v != 1<<32-1 {
		t.Fatalf("Select(2^32-1) = (%d, %v), 期望 (2^32-1, nil)", v, err)
	}
}

// TestAndBitmapToArray 覆盖位图交集得到小结果后转为数组。
func TestAndBitmapToArray(t *testing.T) {
	a := New()
	fillContainer(a, 0, 5000) // 位图容器
	mustStats(t, a, 0, 1, "5000 元素为位图")

	b := New()
	for i := 0; i < 100; i++ {
		b.Add(uint32(i * 7)) // 与 a 的交集为 100 个元素
	}
	mustStats(t, b, 1, 0, "100 元素为数组")

	out := a.And(b)
	if got := out.Cardinality(); got != 100 {
		t.Fatalf("交集基数 = %d, 期望 100", got)
	}
	mustStats(t, out, 1, 0, "位图交出 100 个元素，结果必须规范化为数组")

	// 交为空的容器不出现。
	c := New()
	fillContainer(c, 5, 10) // 键 5，与 a、b 的键 0 不相交
	out2 := a.And(c)
	mustStats(t, out2, 0, 0, "交为空的容器不出现")
	if got := out2.Cardinality(); got != 0 {
		t.Fatalf("空交集基数 = %d, 期望 0", got)
	}
}

// TestRankSelectInverse 覆盖 Rank 与 Select 互逆。
func TestRankSelectInverse(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 3000; i++ {
		s.Add(rng.Uint32())
	}
	if err := s.AddRange(1<<16, (1<<16)+5000); err != nil { // 制造一个位图容器
		t.Fatal(err)
	}
	n := s.Cardinality()
	// 对每个 k：Rank(Select(k)) == k+1，且 Select(Rank(x)-1) == x（x 在集合中）。
	for _, k := range []int{0, 1, n / 3, n / 2, n - 2, n - 1} {
		x, err := s.Select(k)
		if err != nil {
			t.Fatalf("Select(%d) 返回错误: %v", k, err)
		}
		r := s.Rank(x)
		t.Logf("互逆判定: Select(%d)=%d, Rank(%d)=%d, 期望 k+1=%d", k, x, x, r, k+1)
		if r != k+1 {
			t.Fatalf("Rank(Select(%d)) = %d, 期望 %d", k, r, k+1)
		}
		y, err := s.Select(r - 1)
		if err != nil || y != x {
			t.Fatalf("Select(Rank(%d)-1) = (%d, %v), 期望 (%d, nil)", x, y, err, x)
		}
	}
}

// TestRankAcrossContainers 覆盖 Rank 跨容器累计。
func TestRankAcrossContainers(t *testing.T) {
	s := New()
	fillContainer(s, 0, 100)  // 键 0: [0, 100)
	fillContainer(s, 2, 50)   // 键 2: [2<<16, 2<<16+50)
	fillContainer(s, 7, 5000) // 键 7: 位图容器
	cases := []struct {
		x    uint32
		want int
	}{
		{0, 1},
		{99, 100},
		{100, 100},                 // 不存在的元素，Rank 仍按 <= x 计数
		{1<<16 - 1, 100},           // 键 0 与键 2 之间的空隙
		{1 << 16, 100},             // 键 1 为空容器
		{2 << 16, 101},             // 跨进键 2
		{2<<16 | 49, 150},          // 键 2 末尾
		{5 << 16, 150},             // 键 2 与键 7 之间
		{7<<16 | 4999, 150 + 5000}, // 位图容器末尾
		{1<<31 - 1, 150 + 5000},    // 超出最大键
	}
	for _, tc := range cases {
		if got := s.Rank(tc.x); got != tc.want {
			t.Errorf("Rank(%d) = %d, 期望 %d", tc.x, got, tc.want)
		}
	}
}

// TestAddRangeRejection 覆盖 AddRange 的拒绝原因、优先级与无副作用。
func TestAddRangeRejection(t *testing.T) {
	s := New()
	fillContainer(s, 0, 10)
	before := s.Cardinality()

	// lo > hi 与 hi > 2^32 同时成立时，必须先报 lo > hi。
	err := s.AddRange(1<<33+1, 1<<33)
	if !errors.Is(err, ErrLoGreaterThanHi) {
		t.Fatalf("AddRange(lo>hi 且 hi 越界) = %v, 期望 ErrLoGreaterThanHi", err)
	}
	// 仅 lo > hi。
	if err := s.AddRange(200, 100); !errors.Is(err, ErrLoGreaterThanHi) {
		t.Fatalf("AddRange(200, 100) = %v, 期望 ErrLoGreaterThanHi", err)
	}
	// 仅 hi > 2^32。
	err = s.AddRange(0, 1<<32+1)
	if !errors.Is(err, ErrHiExceedsMax) {
		t.Fatalf("AddRange(0, 2^32+1) = %v, 期望 ErrHiExceedsMax", err)
	}
	if errors.Is(err, ErrLoGreaterThanHi) || errors.Is(err, ErrSelectOutOfRange) {
		t.Fatalf("错误原因不可区分: %v", err)
	}
	// lo == hi 是合法空操作（包括 hi == 2^32 的边界）。
	if err := s.AddRange(500, 500); err != nil {
		t.Fatalf("AddRange(500, 500) = %v, 期望 nil", err)
	}
	if err := s.AddRange(1<<32, 1<<32); err != nil {
		t.Fatalf("AddRange(2^32, 2^32) = %v, 期望 nil", err)
	}
	// 被拒绝与空操作均不得改变集合。
	if got := s.Cardinality(); got != before {
		t.Fatalf("拒绝/空操作后 Cardinality = %d, 期望 %d", got, before)
	}
	mustStats(t, s, 1, 0, "被拒绝的操作不得改变集合")
	for i := uint32(0); i < 10; i++ {
		if !s.Contains(i) {
			t.Fatalf("被拒绝的操作改变了集合，缺少 %d", i)
		}
	}
}

// TestSelectOutOfRange 覆盖 Select 越界的可区分错误与无副作用。
func TestSelectOutOfRange(t *testing.T) {
	s := New()
	fillContainer(s, 0, 10)
	for _, k := range []int{-1, 10, 11, 1 << 30} {
		if _, err := s.Select(k); !errors.Is(err, ErrSelectOutOfRange) {
			t.Fatalf("Select(%d) 错误 = %v, 期望 ErrSelectOutOfRange", k, err)
		}
	}
	empty := New()
	if _, err := empty.Select(0); !errors.Is(err, ErrSelectOutOfRange) {
		t.Fatalf("空集合 Select(0) = %v, 期望 ErrSelectOutOfRange", err)
	}
	if got := s.Cardinality(); got != 10 {
		t.Fatalf("Select 越界后 Cardinality = %d, 期望 10", got)
	}
}

// TestDeterministicRepresentation 验证表示类型只取决于元素集合，
// 与增删历史无关；相同操作序列重放结果完全相同。
func TestDeterministicRepresentation(t *testing.T) {
	build := func(ops func(s *Set)) *Set {
		s := New()
		ops(s)
		return s
	}
	// 同一元素集合 {0..4999} 的三种不同历史。
	direct := build(func(s *Set) { fillContainer(s, 0, 5000) })
	ranged := build(func(s *Set) {
		if err := s.AddRange(0, 5000); err != nil {
			t.Fatal(err)
		}
	})
	overshoot := build(func(s *Set) {
		if err := s.AddRange(0, 9000); err != nil { // 先超量再删回
			t.Fatal(err)
		}
		for i := uint32(5000); i < 9000; i++ {
			s.Remove(i)
		}
	})
	for name, s := range map[string]*Set{"direct": direct, "ranged": ranged, "overshoot": overshoot} {
		mustStats(t, s, 0, 1, name+": 同一元素集合表示必须相同（5000 > 4096 为位图）")
		if got := s.Cardinality(); got != 5000 {
			t.Fatalf("%s: Cardinality = %d, 期望 5000", name, got)
		}
	}

	// 相同操作序列重放：逐元素断言 Rank/Select/Stats 完全一致。
	replay := func() *Set {
		s := New()
		rng := rand.New(rand.NewSource(7))
		for i := 0; i < 2000; i++ {
			switch rng.Intn(3) {
			case 0:
				s.Add(rng.Uint32() % 300000)
			case 1:
				s.Remove(rng.Uint32() % 300000)
			case 2:
				lo := uint64(rng.Uint32() % 300000)
				if err := s.AddRange(lo, lo+uint64(rng.Intn(9000))); err != nil {
					t.Fatal(err)
				}
			}
		}
		return s
	}
	r1, r2 := replay(), replay()
	a1, b1 := r1.Stats()
	a2, b2 := r2.Stats()
	if a1 != a2 || b1 != b2 || r1.Cardinality() != r2.Cardinality() {
		t.Fatalf("重放不一致: (%d,%d,%d) vs (%d,%d,%d)",
			a1, b1, r1.Cardinality(), a2, b2, r2.Cardinality())
	}
	for i := 0; i < r1.Cardinality(); i += 97 {
		v1, _ := r1.Select(i)
		v2, _ := r2.Select(i)
		if v1 != v2 {
			t.Fatalf("重放 Select(%d) 不一致: %d vs %d", i, v1, v2)
		}
	}
}

// TestFuzzAgainstNaive 用有序切片朴素集合对拍 2000 组随机操作，
// 每步打印输入、输出与判定依据。
func TestFuzzAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261015))
	got := New()
	want := &naiveSet{}
	other := New()
	otherWant := &naiveSet{}
	// 预置另一个操作数集合，供 And 对拍。
	for i := 0; i < 300; i++ {
		x := rng.Uint32() % 200000
		other.Add(x)
		otherWant.Add(x)
	}

	check := func(step int, op string) {
		t.Helper()
		gc, wc := got.Cardinality(), want.Cardinality()
		ga, gb := got.Stats()
		wa, wb := want.Stats()
		t.Logf("step %d op=%s | 输出: card=%d stats=(%d,%d) | 期望: card=%d stats=(%d,%d) | 依据: 表示类型是元素集合的纯函数",
			step, op, gc, ga, gb, wc, wa, wb)
		if gc != wc {
			t.Fatalf("step %d (%s): Cardinality = %d, 朴素模型 = %d", step, op, gc, wc)
		}
		if ga != wa || gb != wb {
			t.Fatalf("step %d (%s): Stats = (%d,%d), 朴素模型推出 (%d,%d)", step, op, ga, gb, wa, wb)
		}
	}

	for step := 0; step < 2000; step++ {
		switch rng.Intn(6) {
		case 0: // Add
			x := rng.Uint32() % 200000
			got.Add(x)
			want.Add(x)
			check(step, "Add "+itoa(x))
		case 1: // Remove（含不存在的元素）
			x := rng.Uint32() % 200000
			got.Remove(x)
			want.Remove(x)
			check(step, "Remove "+itoa(x))
		case 2: // AddRange（长度覆盖 4096 边界两侧）
			lo := uint64(rng.Uint32() % 190000)
			hi := lo + uint64(rng.Intn(12000))
			if err := got.AddRange(lo, hi); err != nil {
				t.Fatalf("step %d: AddRange(%d, %d) = %v", step, lo, hi, err)
			}
			want.AddRange(lo, hi)
			check(step, "AddRange ["+itoa64(lo)+", "+itoa64(hi)+")")
		case 3: // Rank
			x := rng.Uint32() % 200000
			g, w := got.Rank(x), want.Rank(x)
			t.Logf("step %d op=Rank(%d) | 输出: %d | 期望: %d | 依据: 不大于 x 的元素个数", step, x, g, w)
			if g != w {
				t.Fatalf("step %d: Rank(%d) = %d, 朴素模型 = %d", step, x, g, w)
			}
		case 4: // Select
			k := rng.Intn(want.Cardinality() + 2) // 覆盖越界
			g, gerr := got.Select(k)
			w, wok := want.Select(k)
			t.Logf("step %d op=Select(%d) | 输出: (%d, %v) | 期望: (%d, ok=%v) | 依据: 第 k 小元素或越界错误",
				step, k, g, gerr, w, wok)
			if wok != (gerr == nil) || (wok && g != w) {
				t.Fatalf("step %d: Select(%d) = (%d, %v), 朴素模型 = (%d, %v)", step, k, g, gerr, w, wok)
			}
		case 5: // And
			inter := got.And(other)
			interWant := want.And(otherWant)
			gc, wc := inter.Cardinality(), interWant.Cardinality()
			ga, gb := inter.Stats()
			wa, wb := interWant.Stats()
			t.Logf("step %d op=And | 输出: card=%d stats=(%d,%d) | 期望: card=%d stats=(%d,%d) | 依据: 逐容器交集后按基数规范化",
				step, gc, ga, gb, wc, wa, wb)
			if gc != wc || ga != wa || gb != wb {
				t.Fatalf("step %d: And 结果 (%d,(%d,%d)), 朴素模型 (%d,(%d,%d))",
					step, gc, ga, gb, wc, wa, wb)
			}
			// 抽验交集内容。
			if gc > 0 {
				k := rng.Intn(gc)
				gv, _ := inter.Select(k)
				wv, _ := interWant.Select(k)
				if gv != wv {
					t.Fatalf("step %d: 交集 Select(%d) = %d, 朴素模型 = %d", step, k, gv, wv)
				}
			}
		}
	}
}

// TestConcurrent 验证并发调用等价于某个串行顺序：
// 各 goroutine 操作互不相交的键区间，最终内容必须等于串行结果。
func TestConcurrent(t *testing.T) {
	s := New()
	const workers = 8
	const perWorker = 5000 // 每 goroutine 制造一个位图容器
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w) << 16
			for i := uint32(0); i < perWorker; i++ {
				s.Add(base | i)
			}
			for i := uint32(0); i < perWorker; i += 2 {
				s.Remove(base | i) // 删到 2500，触发位图转数组
			}
			for i := uint32(1); i < perWorker; i += 2 {
				if !s.Contains(base | i) {
					t.Errorf("并发后缺少元素 %d", base|i)
				}
				_ = s.Rank(base | i)
			}
			if err := s.AddRange(uint64(base), uint64(base)+100); err != nil {
				t.Errorf("并发 AddRange: %v", err)
			}
		}(w)
	}
	// 并发执行 And（只读，与写操作交错）。
	probe := New()
	probe.AddRange(0, 3000)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.And(probe).Cardinality()
			_ = probe.And(s).Cardinality() // 反向 And 验证锁顺序无死锁
		}()
	}
	wg.Wait()

	// 每个容器最终为 [0,100) ∪ {奇数 < 5000}，基数 100+2450=2550 <= 4096，必为数组。
	mustStats(t, s, workers, 0, "并发结果等价于串行顺序：每容器 2550 元素为数组")
	if got := s.Cardinality(); got != workers*2550 {
		t.Fatalf("Cardinality = %d, 期望 %d", got, workers*2550)
	}
}

func itoa(x uint32) string {
	return itoa64(uint64(x))
}

func itoa64(x uint64) string {
	if x == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for x > 0 {
		i--
		buf[i] = byte('0' + x%10)
		x /= 10
	}
	return string(buf[i:])
}
