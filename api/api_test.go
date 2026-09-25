package api

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func feedInts(m *Summary, xs ...int) error {
	keys := make([]Key, len(xs))
	for i, x := range xs {
		keys[i] = Key(x)
	}
	return m.Feed(keys)
}

type streamCase struct {
	k      int
	events []int
}

// TestEightStep 钉住 NOTES.md 八行表的最终状态与三问结论；SelfCheck 必须直接通过。
func TestEightStep(t *testing.T) {
	m, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := feedInts(m, 3, 1, 3, 2, 4, 1, 3, 5); err != nil {
		t.Fatal(err)
	}
	const want = "[{3 3 0} {5 3 2} {4 2 1}]" // Count 降序、Key 升序
	if got := fmt.Sprint(m.TopK()); got != want {
		t.Fatalf("TopK = %s，want %s", got, want)
	}
	if q5, q1, q2 := m.Query(5), m.Query(1), m.Query(2); q5 != 3 || q1 != 0 || q2 != 0 {
		t.Fatalf("Query(5/1/2) = %d/%d/%d，want 3/0/0", q5, q1, q2)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck = %v，want nil", err)
	}
}

// TestInvariants 表驱动 + 循环生成多档事件流，核验不变量 1/2/3。
func TestInvariants(t *testing.T) {
	cases := []streamCase{{3, []int{3, 1, 3, 2, 4, 1, 3, 5}}}
	for _, k := range []int{1, 3, 10, 50} { // 循环生成多档规模与随机感事件流
		for _, mod := range []int{5, 97} {
			ev := make([]int, 3000)
			for i := range ev {
				ev[i] = (i*i + 7) % mod
			}
			cases = append(cases, streamCase{k, ev})
		}
	}
	for _, tc := range cases {
		m, _ := New(tc.k)
		trueCnt := map[int]int{}
		for _, x := range tc.events {
			feedInts(m, x) // 事件均非负，不会失败
			trueCnt[x]++
			if len(m.TopK()) > tc.k { // 不变量 3：容量恒定
				t.Fatalf("k=%d: 计数器个数超过 k", tc.k)
			}
		}
		minCount, monitored := -1, map[int]bool{}
		for _, e := range m.TopK() {
			monitored[e.Key] = true
			if e.Count < trueCnt[e.Key] || e.Count-e.Err > trueCnt[e.Key] { // 不变量 1
				t.Fatalf("k=%d Key=%d: 违反误差界 %+v", tc.k, e.Key, e)
			}
			if minCount < 0 || e.Count < minCount {
				minCount = e.Count
			}
		}
		for key, tru := range trueCnt {
			if !monitored[key] && tru > minCount { // 不变量 2：最小计数下界
				t.Fatalf("k=%d Key=%d: 未监控键真实计数 %d > 最小 count %d", tc.k, key, tru, minCount)
			}
		}
	}
}

// TestFaultInjection 核验三类哨兵错误互不相同、失败不留痕（不变量 4）。
func TestFaultInjection(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(0) err = %v，want ErrInvalidK", err)
	}
	m, _ := New(3)
	if err := feedInts(m, 1, 2, 2); err != nil {
		t.Fatal(err)
	}
	before := fmt.Sprint(m.TopK())
	cases := []struct {
		name string
		feed []Key
		want error
	}{
		{"nil 切片", nil, ErrNilFeed},
		{"负数 Key", []Key{5, -1, 6}, ErrNegativeKey},
	}
	for _, tc := range cases {
		if err := m.Feed(tc.feed); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v，want %v", tc.name, err, tc.want)
		}
		if fmt.Sprint(m.TopK()) != before {
			t.Fatalf("%s: 被拒后状态改变", tc.name)
		}
	}
	if errors.Is(ErrInvalidK, ErrNilFeed) || errors.Is(ErrNilFeed, ErrNegativeKey) ||
		errors.Is(ErrInvalidK, ErrNegativeKey) {
		t.Fatal("哨兵错误两两不可区分")
	}
	if err := feedInts(m, 9); err != nil || m.Query(9) != 1 { // 被拒后可继续用
		t.Fatalf("被拒后无法继续正常使用: err=%v Query(9)=%d", err, m.Query(9))
	}
}

// TestConcurrentReadConsistency N 个 goroutine 并发只读，结果逐字段相同（不用 sleep）。
func TestConcurrentReadConsistency(t *testing.T) {
	m, _ := New(10)
	for i := 0; i < 500; i++ {
		feedInts(m, (i*i+3)%23) // 不会失败
	}
	gold, goldS := m.TopK(), fmt.Sprint(m.TopK())
	last := gold[len(gold)-1]
	start := make(chan struct{})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				if fmt.Sprint(m.TopK()) != goldS ||
					m.Query(Key(gold[0].Key)) != gold[0].Count ||
					m.Query(Key(last.Key)) != last.Count {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Fatal("并发只读结果不一致")
	}
}
