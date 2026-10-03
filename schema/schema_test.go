package schema_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/schema"
)

func TestRegisterVersionChain(t *testing.T) {
	reg := schema.NewRegistry()
	v, err := reg.Register("lat", []int64{10, 20, 50, 100})
	if err != nil || v != 1 {
		t.Fatalf("v1: got (%d, %v)", v, err)
	}
	v, err = reg.Register("lat", []int64{20, 100}) // 真子集
	if err != nil || v != 2 {
		t.Fatalf("v2: got (%d, %v)", v, err)
	}
	if _, err = reg.Register("lat", []int64{20, 50, 70}); !errors.Is(err, schema.ErrIncompatible) {
		t.Fatalf("incompatible: got %v", err)
	}
	if _, err = reg.Register("lat", []int64{20, 100}); !errors.Is(err, schema.ErrNoChange) {
		t.Fatalf("no change: got %v", err)
	}
	v, err = reg.Register("lat", []int64{10, 20, 40, 100, 200}) // 真超集
	if err != nil || v != 3 {
		t.Fatalf("v3: got (%d, %v)", v, err)
	}
	for tv, want := range map[int][]int64{1: {10, 20, 50, 100}, 2: {20, 100}, 3: {10, 20, 40, 100, 200}} { // 旧版本永久保留
		got, err := reg.Bounds("lat", tv)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("v%d: got %v, %v", tv, got, err)
		}
	}
}

func TestRegisterInvalidAndOrder(t *testing.T) {
	reg := schema.NewRegistry()
	cases := map[string][]int64{
		"":                      {1},                 // 空名
		strings.Repeat("x", 65): {1},                 // 名超 64 字节
		"n1":                    {},                  // 空边界
		"n2":                    {0},                 // 边界 < 1
		"n3":                    {1_000_000_000_001}, // 边界 > 10^12
		"n4":                    {5, 5},              // 非严格递增
		"n5":                    {9, 3},              // 逆序
	}
	for name, bounds := range cases {
		if _, err := reg.Register(name, bounds); !errors.Is(err, schema.ErrInvalid) {
			t.Fatalf("name=%q bounds=%v: got %v", name, bounds, err)
		}
	}
	tooMany := make([]int64, 65)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	if _, err := reg.Register("n6", tooMany); !errors.Is(err, schema.ErrInvalid) {
		t.Fatalf("65 bounds: got %v", err)
	}
	if _, err := reg.Register("ok", []int64{1, 2}); err != nil { // 拒绝顺序：参数非法优先
		t.Fatal(err)
	}
	if _, err := reg.Register("ok", []int64{2, 1}); !errors.Is(err, schema.ErrInvalid) {
		t.Fatalf("invalid before nochange: got %v", err)
	}
	if _, err := reg.Bounds("ok", 0); !errors.Is(err, schema.ErrNotFound) {
		t.Fatalf("version 0: got %v", err)
	}
	if _, err := reg.Bounds("ghost", 1); !errors.Is(err, schema.ErrNotFound) {
		t.Fatalf("unknown name: got %v", err)
	}
}

func TestBoundsReturnsCopy(t *testing.T) {
	reg := schema.NewRegistry()
	if _, err := reg.Register("c", []int64{5, 9}); err != nil {
		t.Fatal(err)
	}
	b, _ := reg.Bounds("c", 1)
	b[0] = 999
	again, _ := reg.Bounds("c", 1)
	if again[0] != 5 {
		t.Fatalf("Bounds 未返回副本: %v", again)
	}
}

func newLatCollector(t *testing.T) *schema.Collector {
	t.Helper()
	reg := schema.NewRegistry()
	if _, err := reg.Register("lat", []int64{10, 20, 50, 100}); err != nil {
		t.Fatal(err)
	}
	c, err := schema.NewCollector(reg, "lat", 1)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCollectorBucketAttribution(t *testing.T) {
	c := newLatCollector(t)
	// 边界恰等归属本桶；桶 0 含 0；溢出桶收 >100
	vals := []int64{0, 10, 11, 20, 50, 99, 100, 101, 1_000_000_000_000}
	for _, v := range vals {
		if err := c.Observe(v); err != nil {
			t.Fatalf("Observe(%d): %v", v, err)
		}
	}
	snap := c.Snapshot()
	want := []int64{2, 2, 1, 2, 2} // ≤10: {0,10}; (10,20]: {11,20}; (20,50]: {50}; (50,100]: {99,100}; 溢出: {101,1e12}
	for i := range want {
		if snap.Counts[i] != want[i] {
			t.Fatalf("bucket %d: got %d want %d (counts=%v)", i, snap.Counts[i], want[i], snap.Counts)
		}
	}
	if snap.Sum != 1_000_000_000_391 { // vals 之和
		t.Fatalf("Sum: got %d", snap.Sum)
	}
	// 快照独立：改快照不影响采集器
	snap.Counts[0] = -1
	snap.Bounds[0] = -1
	again := c.Snapshot()
	if again.Counts[0] != 2 || again.Bounds[0] != 10 {
		t.Fatalf("Snapshot 非独立副本: %v", again)
	}
}

func TestObserveRangeAndOverflow(t *testing.T) {
	c := newLatCollector(t)
	for _, v := range []int64{-1, 1_000_000_000_001, math.MaxInt64} {
		if err := c.Observe(v); !errors.Is(err, schema.ErrInvalid) {
			t.Fatalf("Observe(%d): got %v", v, err)
		}
	}
	if _, err := schema.NewCollector(schema.NewRegistry(), "ghost", 1); !errors.Is(err, schema.ErrNotFound) {
		t.Fatalf("NewCollector: got %v", err)
	}
	n := int64(math.MaxInt64 / 1_000_000_000_000) // Sum 溢出：观测 10^12 可容纳的次数
	for i := int64(0); i < n; i++ {
		if err := c.Observe(1_000_000_000_000); err != nil {
			t.Fatalf("observe %d: %v", i, err)
		}
	}
	if err := c.Observe(1_000_000_000_000); !errors.Is(err, schema.ErrOverflow) {
		t.Fatalf("overflow: got %v", err)
	}
	snap := c.Snapshot() // 溢出不得改任何状态
	if snap.Sum != n*1_000_000_000_000 || snap.Counts[len(snap.Counts)-1] != n {
		t.Fatalf("state changed on overflow: sum=%d counts=%v", snap.Sum, snap.Counts)
	}
}

func TestConcurrentObserveSnapshot(t *testing.T) {
	c := newLatCollector(t)
	const workers = 8
	const perWorker = 1200 // 每 worker 观测值为 i%120，和固定
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := c.Observe(int64(i % 120)); err != nil {
					t.Errorf("observe: %v", err)
					return
				}
				if i%100 == 0 {
					snap := c.Snapshot() // 快照须为某 Observe 前缀的一致视图
					var total int64
					for _, n := range snap.Counts {
						total += n
					}
					if err := snap.Validate(); err != nil {
						t.Errorf("snapshot invalid: %v", err)
						return
					}
					if total < 0 || total > workers*perWorker || snap.Sum < 0 {
						t.Errorf("inconsistent snapshot: %v", snap)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	snap := c.Snapshot()
	var total int64
	for _, n := range snap.Counts {
		total += n
	}
	sum := int64(workers * (perWorker / 120) * (120 * 119 / 2)) // 每 worker 为 10 个完整 0..119 周期
	if total != workers*perWorker || snap.Sum != sum {
		t.Fatalf("total=%d sum=%d, want total=%d sum=%d", total, snap.Sum, workers*perWorker, sum)
	}
}
