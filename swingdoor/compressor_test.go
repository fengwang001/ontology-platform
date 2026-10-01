package swingdoor

import (
	"errors"
	"math/big"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// run 用给定容差顺序写入全部采样并关闭，返回存档点序列。
func run(t *testing.T, e int64, pts []Point) []Point {
	t.Helper()
	c, err := New(e)
	if err != nil {
		t.Fatalf("New(%d): %v", e, err)
	}
	for _, p := range pts {
		if err := c.Write(p.T, p.V); err != nil {
			t.Fatalf("Write(%d, %d): %v", p.T, p.V, err)
		}
	}
	arch, err := c.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return arch
}

// verify 用有理数（big.Rat）逐点校验：每个被丢弃的采样到其前后
// 相邻存档点连线的纵向偏差不超过 e。同时校验存档点是输入的子序列、
// 首尾采样都在存档点中、时间戳严格递增，并打印判定依据。
func verify(t *testing.T, e int64, input, arch []Point) {
	t.Helper()
	t.Logf("输入: %v", input)
	t.Logf("存档: %v", arch)

	if len(arch) == 0 {
		t.Fatal("存档点为空")
	}
	if arch[0] != input[0] {
		t.Fatalf("首个采样 %v 不在存档点首位（实际 %v）", input[0], arch[0])
	}
	if arch[len(arch)-1] != input[len(input)-1] {
		t.Fatalf("末个采样 %v 不在存档点末位（实际 %v）",
			input[len(input)-1], arch[len(arch)-1])
	}
	for i := 1; i < len(arch); i++ {
		if arch[i].T <= arch[i-1].T {
			t.Fatalf("存档点时间戳非严格递增: %v -> %v", arch[i-1], arch[i])
		}
	}

	tol := big.NewRat(e, 1)
	ai := 0
	for _, p := range input {
		if ai < len(arch) && arch[ai] == p {
			t.Logf("保留 %v：存档点", p)
			ai++
			continue
		}
		if ai == 0 || ai >= len(arch) {
			t.Fatalf("丢弃点 %v 缺少相邻存档点", p)
		}
		a, b := arch[ai-1], arch[ai]
		if !(a.T < p.T && p.T < b.T) {
			t.Fatalf("丢弃点 %v 不在存档段 (%v, %v) 之间", p, a, b)
		}
		// 连线在 p.T 处的取值：a.V + (b.V-a.V)*(p.T-a.T)/(b.T-a.T)
		line := new(big.Rat).SetInt64(a.V)
		term := new(big.Rat).SetFrac(big.NewInt(b.V-a.V), big.NewInt(b.T-a.T))
		term.Mul(term, new(big.Rat).SetInt64(p.T-a.T))
		line.Add(line, term)
		dev := new(big.Rat).Sub(new(big.Rat).SetInt64(p.V), line)
		dev.Abs(dev)
		t.Logf("丢弃 %v：相对存档段 %v -> %v 的纵向偏差 %s（E=%d）",
			p, a, b, dev.RatString(), e)
		if dev.Cmp(tol) > 0 {
			t.Errorf("丢弃点 %v 偏差 %s 超过容差 %d", p, dev.RatString(), e)
		}
	}
	if ai != len(arch) {
		t.Fatalf("存档点 %v 不是输入的子序列", arch)
	}
}

func TestSlopeExactlyAtBounds(t *testing.T) {
	// E=2，起点 (0,0)。(1,0) 使 lo=-2、hi=2。
	t.Run("恰等于hi继续", func(t *testing.T) {
		pts := []Point{{0, 0}, {1, 0}, {2, 4}}
		// (2,4) 斜率 2 恰等于 hi=2，仍可继续，不触发存档。
		arch := run(t, 2, pts)
		want := []Point{{0, 0}, {2, 4}}
		if !reflect.DeepEqual(arch, want) {
			t.Fatalf("got %v, want %v", arch, want)
		}
		verify(t, 2, pts, arch)
	})
	t.Run("恰等于lo继续", func(t *testing.T) {
		pts := []Point{{0, 0}, {1, 0}, {2, -4}}
		// (2,-4) 斜率 -2 恰等于 lo=-2，仍可继续，不触发存档。
		arch := run(t, 2, pts)
		want := []Point{{0, 0}, {2, -4}}
		if !reflect.DeepEqual(arch, want) {
			t.Fatalf("got %v, want %v", arch, want)
		}
		verify(t, 2, pts, arch)
	})
}

func TestZeroTolerance(t *testing.T) {
	// E=0：共线点被丢弃，折点被保留。
	pts := []Point{{0, 0}, {1, 1}, {2, 2}, {3, 2}}
	arch := run(t, 0, pts)
	want := []Point{{0, 0}, {2, 2}, {3, 2}}
	if !reflect.DeepEqual(arch, want) {
		t.Fatalf("got %v, want %v", arch, want)
	}
	verify(t, 0, pts, arch)
}

func TestRestartAnchorIsPreviousSample(t *testing.T) {
	// 反例：E=1，(0,0) (1,0) (2,3) (3,0)。
	// (2,3) 斜率 3/2 超出 hi=1，存档的必须是上一采样 (1,0)，
	// 而不是把 (2,3) 直接存档（否则 (1,0) 偏差 1.5 > E）。
	pts := []Point{{0, 0}, {1, 0}, {2, 3}, {3, 0}}
	arch := run(t, 1, pts)
	want := []Point{{0, 0}, {1, 0}, {2, 3}, {3, 0}}
	if !reflect.DeepEqual(arch, want) {
		t.Fatalf("got %v, want %v", arch, want)
	}
	if arch[1] != (Point{1, 0}) {
		t.Fatalf("重启起点应为上一采样 (1,0)，实际 %v", arch[1])
	}
	verify(t, 1, pts, arch)
}

func TestSingleSample(t *testing.T) {
	pts := []Point{{5, 7}}
	arch := run(t, 3, pts)
	want := []Point{{5, 7}}
	if !reflect.DeepEqual(arch, want) {
		t.Fatalf("got %v, want %v", arch, want)
	}
	verify(t, 3, pts, arch)
}

func TestTwoSamples(t *testing.T) {
	pts := []Point{{0, 0}, {10, 3}}
	arch := run(t, 0, pts)
	want := []Point{{0, 0}, {10, 3}}
	if !reflect.DeepEqual(arch, want) {
		t.Fatalf("got %v, want %v", arch, want)
	}
	verify(t, 0, pts, arch)
}

func TestCloseWhenLastIsArchive(t *testing.T) {
	// 只有一个采样时，末点已是存档点，关闭不得重复追加。
	c, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(9, -9); err != nil {
		t.Fatal(err)
	}
	arch, err := c.Close()
	if err != nil {
		t.Fatal(err)
	}
	want := []Point{{9, -9}}
	if !reflect.DeepEqual(arch, want) {
		t.Fatalf("got %v, want %v", arch, want)
	}
}

func TestRejections(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrInvalidTolerance) {
		t.Fatalf("负容差: %v", err)
	}
	if _, err := New(MaxTolerance + 1); !errors.Is(err, ErrInvalidTolerance) {
		t.Fatalf("容差超限: %v", err)
	}
	if _, err := New(MaxTolerance); err != nil {
		t.Fatalf("边界容差应合法: %v", err)
	}

	c, _ := New(1)
	mustWrite := func(p Point) {
		t.Helper()
		if err := c.Write(p.T, p.V); err != nil {
			t.Fatalf("Write(%v): %v", p, err)
		}
	}
	mustWrite(Point{0, 0})
	mustWrite(Point{1, 1})
	before := c.Points()

	// 数值超限（时间戳与数值分别检查）。
	if err := c.Write(2, MaxAbsValue+1); !errors.Is(err, ErrValueOutOfRange) {
		t.Fatalf("数值超限: %v", err)
	}
	if err := c.Write(2, -MaxAbsValue-1); !errors.Is(err, ErrValueOutOfRange) {
		t.Fatalf("数值负向超限: %v", err)
	}
	if err := c.Write(MaxAbsValue+1, 0); !errors.Is(err, ErrValueOutOfRange) {
		t.Fatalf("时间戳超限: %v", err)
	}
	// 时间次序：等于与小于上一采样是两种不同原因。
	if err := c.Write(1, 5); !errors.Is(err, ErrTimestampEqual) {
		t.Fatalf("时间戳相等: %v", err)
	}
	if err := c.Write(0, 5); !errors.Is(err, ErrTimestampBackward) {
		t.Fatalf("时间戳回退: %v", err)
	}
	// 数值超限优先于时间次序。
	if err := c.Write(0, MaxAbsValue+1); !errors.Is(err, ErrValueOutOfRange) {
		t.Fatalf("检查顺序应为数值超限优先: %v", err)
	}
	// 被拒绝的操作不得改变状态。
	if got := c.Points(); !reflect.DeepEqual(got, before) {
		t.Fatalf("拒绝后状态改变: %v -> %v", before, got)
	}
	// 合法写入仍可进行，流未被污染。
	mustWrite(Point{2, 2})
	arch, err := c.Close()
	if err != nil {
		t.Fatal(err)
	}
	verify(t, 1, []Point{{0, 0}, {1, 1}, {2, 2}}, arch)
}

func TestEmptyStreamClose(t *testing.T) {
	c, _ := New(0)
	if _, err := c.Close(); !errors.Is(err, ErrEmptyStream) {
		t.Fatalf("空流关闭: %v", err)
	}
}

func TestWriteAfterClose(t *testing.T) {
	c, _ := New(1)
	if err := c.Write(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(1, 1); !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("关闭后写入: %v", err)
	}
	if _, err := c.Close(); !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("重复关闭: %v", err)
	}
	// 已关闭优先于数值超限。
	if err := c.Write(MaxAbsValue+1, 0); !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("检查顺序应为已关闭优先: %v", err)
	}
}

func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	pts := make([]Point, 500)
	for i := range pts {
		pts[i] = Point{T: int64(i), V: int64(rng.Intn(2001) - 1000)}
	}
	first := run(t, 100, pts)
	for i := 0; i < 5; i++ {
		if got := run(t, 100, pts); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次重放不一致", i)
		}
	}
	verify(t, 100, pts, first)
}

func TestLargeValuesExactArithmetic(t *testing.T) {
	// 交叉相乘的乘积可超过 int64（约 1.2e19 > 9.2e18），
	// 必须走 big.Int 路径仍保持精确。
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		n := 2 + rng.Intn(30)
		pts := make([]Point, n)
		last := int64(-MaxAbsValue - 1)
		for i := range pts {
			// 稀疏大时间戳、大数值、大容差。
			last += rng.Int63n(MaxAbsValue/int64(n)) + 1
			pts[i] = Point{T: last, V: int64(rng.Int63n(2*MaxAbsValue+1) - MaxAbsValue)}
		}
		e := int64(rng.Int63n(MaxTolerance + 1))
		arch := run(t, e, pts)
		verify(t, e, pts, arch)
	}
}

func TestRandomAgainstNaiveVerifier(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		n := 1 + rng.Intn(60)
		pts := make([]Point, n)
		v := int64(rng.Intn(21) - 10)
		for i := range pts {
			v += int64(rng.Intn(11) - 5)
			pts[i] = Point{T: int64(i), V: v}
		}
		e := int64(rng.Intn(6))
		arch := run(t, e, pts)
		t.Logf("trial=%d E=%d 输入 %d 点 -> 存档 %d 点", trial, e, n, len(arch))
		verify(t, e, pts, arch)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c, _ := New(10)
	const n = 1000
	var wg sync.WaitGroup
	// 一个写协程写递增时间戳，多个读协程并发查询。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := c.Write(int64(i), int64(i%50)); err != nil {
				t.Errorf("Write: %v", err)
				return
			}
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev := -1
			for i := 0; i < n; i++ {
				pts := c.Points()
				if len(pts) < prev {
					t.Errorf("存档点数量回退: %d -> %d", prev, len(pts))
					return
				}
				prev = len(pts)
				for j := 1; j < len(pts); j++ {
					if pts[j].T <= pts[j-1].T {
						t.Errorf("并发读到的存档点时间戳非递增")
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	arch, err := c.Close()
	if err != nil {
		t.Fatal(err)
	}
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{T: int64(i), V: int64(i % 50)}
	}
	verify(t, 10, pts, arch)
	// 并发读写的结果应与串行执行一致。
	if want := run(t, 10, pts); !reflect.DeepEqual(arch, want) {
		t.Fatalf("并发结果与串行不一致: %v vs %v", arch, want)
	}
}
