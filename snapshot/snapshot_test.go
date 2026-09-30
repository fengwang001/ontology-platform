package snapshot

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func requireValues(t *testing.T, got, want []int, reason string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got length %d, want %d; output=%v", reason, len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: output=%v, want=%v", reason, got, want)
		}
	}
	t.Logf("判定通过 %s: 输出=%v", reason, got)
}

func TestConstructionAndRejectedUpdates(t *testing.T) {
	for _, n := range []int{-1, 0, 17, 18} {
		_, err := New(n)
		if !errors.Is(err, ErrInvalidN) {
			t.Fatalf("输入 New(%d): err=%v, want ErrInvalidN", n, err)
		}
		t.Logf("输入 New(%d): 输出 err=%v；判定：越界被拒绝", n, err)
	}

	s, err := New(2)
	if err != nil {
		t.Fatal(err)
	}

	err = s.Update(0, 2, 1)
	if !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("越界下标: err=%v, want ErrInvalidIndex", err)
	}
	t.Logf("输入 Update(writer=0,index=2,value=1): 输出 err=%v；判定：先报下标越界", err)

	err = s.Update(0, 1, 1)
	if !errors.Is(err, ErrInvalidCaller) {
		t.Fatalf("调用者不符: err=%v, want ErrInvalidCaller", err)
	}
	t.Logf("输入 Update(writer=0,index=1,value=1): 输出 err=%v；判定：调用者与单元不符", err)

	requireValues(t, s.Read(), []int{0, 0}, "两次拒绝后的状态必须保持初值")

	if err := s.Update(1, 1, 7); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入 Update(writer=1,index=1,value=7): 输出 err=nil；判定：合法更新被接受")
	requireValues(t, s.Read(), []int{0, 7}, "合法更新后的状态")
}

func TestCausalChain(t *testing.T) {
	s, _ := New(2)
	if err := s.Update(0, 0, 10); err != nil {
		t.Fatal(err)
	}
	t.Logf("写者0输入 value=10；返回后允许后续读者观察")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			view := s.Read()
			if view[0] == 10 {
				t.Logf("写者1观察到快照=%v，因此输入 value=20", view)
				if err := s.Update(1, 1, 20); err != nil {
					t.Error(err)
				}
				return
			}
		}
	}()
	<-done

	for i := 0; i < 100; i++ {
		view := s.Read()
		if view[1] == 20 && view[0] != 10 {
			t.Fatalf("因果性被破坏: 快照=%v 出现新 y=20 但 x 仍旧", view)
		}
	}
	t.Log("判定通过：100 次快照中凡出现 y=20，x 均为 10，不存在 y 新而 x 旧")
}

func TestMonotonicSnapshots(t *testing.T) {
	s, _ := New(3)
	previous := s.Read()
	t.Logf("初始 Read 输出=%v", previous)

	for round := 1; round <= 20; round++ {
		for writer := 0; writer < 3; writer++ {
			if err := s.Update(writer, writer, round); err != nil {
				t.Fatal(err)
			}
		}
		current := s.Read()
		t.Logf("输入 rounds<=%d；Read 输出=%v；判定依据：A 已完成后 B 才开始", round, current)
		for i := range current {
			if current[i] < previous[i] {
				t.Fatalf("单调性被破坏: previous=%v current=%v", previous, current)
			}
		}
		previous = current
	}
}

func TestSingleUnit(t *testing.T) {
	s, _ := New(1)
	var writers sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})

	writers.Add(1)
	go func() {
		defer writers.Done()
		for value := 1; ; value++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := s.Update(0, 0, value); err != nil {
				t.Error(err)
				return
			}
		}
	}()

	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 100; i++ {
				view := s.Read()
				if len(view) != 1 || view[0] < 0 {
					t.Errorf("n=1 快照非法: %v", view)
					return
				}
			}
		}()
	}

	readers.Wait()
	close(stop)
	writers.Wait()

	if got := s.MaxCollections(); got > 3 {
		t.Fatalf("n=1 收集次数上界被破坏: max=%d, want<=3", got)
	}
	t.Logf("持续更新时 MaxCollections=%d；判定依据：2n+1=%d", s.MaxCollections(), 3)

	if err := s.Update(0, 0, 4242); err != nil {
		t.Fatal(err)
	}
	requireValues(t, s.Read(), []int{4242}, "更新返回后立即读必须可见")
}

func TestContinuousWritersCollectionBound(t *testing.T) {
	const n = 4
	s, _ := New(n)
	var writers sync.WaitGroup
	var readers sync.WaitGroup

	for writer := 0; writer < n; writer++ {
		writers.Add(1)
		go func(writer int) {
			defer writers.Done()
			for step := 1; step <= 1000; step++ {
				value := step*n + writer + 1
				if err := s.Update(writer, writer, value); err != nil {
					t.Error(err)
					return
				}
			}
		}(writer)
	}

	for reader := 0; reader < 8; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 500; i++ {
				view := s.Read()
				if len(view) != n {
					t.Errorf("快照长度非法: %d", len(view))
					return
				}
				for cell, value := range view {
					if value != 0 && value%n != (cell+1)%n {
						t.Errorf("单元 %d 出现从未写入的值 %d", cell, value)
						return
					}
				}
			}
		}()
	}

	writers.Wait()
	readers.Wait()

	upperBound := uint64(2*n + 1)
	if got := s.MaxCollections(); got > upperBound {
		t.Fatalf("收集次数上界被破坏: max=%d, want<=%d", got, upperBound)
	}
	final := s.Read()
	t.Logf("持续写入结束: 最终快照=%v MaxCollections=%d；判定依据：每次借用后立即返回且上界 2n+1=%d", final, s.MaxCollections(), upperBound)
}

type timedRead struct {
	start time.Time
	end   time.Time
	view  []int
	point uint64
}

func TestConcurrentSnapshotInterpretability(t *testing.T) {
	const n = 8
	s, _ := New(n)

	var mu sync.Mutex
	var reads []timedRead

	stop := make(chan struct{})
	var wg sync.WaitGroup

	for writer := 0; writer < n; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			seq := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				seq++
				err := s.Update(writer, writer, seq)
				if err != nil {
					t.Error(err)
					return
				}
			}
		}(writer)
	}

	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				start := time.Now()
				view, point := s.readForTest()
				end := time.Now()
				mu.Lock()
				reads = append(reads, timedRead{start: start, end: end, view: append([]int(nil), view...), point: point})
				mu.Unlock()
			}
		}()
	}

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()

	history := make([][]int, s.commit.Load()+1)
	for point := range history {
		history[point] = make([]int, n)
	}
	for cell := 0; cell < n; cell++ {
		for current := s.records[cell].Load(); current != nil && current.commit != 0; current = current.prev {
			history[current.commit][cell] = current.value
		}
		lastValue := 0
		for point := 0; point < len(history); point++ {
			if history[point][cell] != 0 {
				lastValue = history[point][cell]
				continue
			}
			history[point][cell] = lastValue
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for _, read := range reads {
		point := read.point
		if point >= uint64(len(history)) {
			t.Fatalf("快照=%v 的提交点 %d 不存在", read.view, point)
		}
		committed := history[point]
		for cell := range read.view {
			if committed[cell] != read.view[cell] {
				t.Fatalf("快照=%v 在提交点 %d 的真实向量=%v", read.view, point, committed)
			}
		}
		t.Logf("并发 Read 输入=无 输出=%v；判定：等于全局提交历史点 %d 的真实向量，调用区间=[%v,%v]", read.view, point, read.start, read.end)
	}

	upperBound := uint64(2*n + 1)
	if got := s.MaxCollections(); got > upperBound {
		t.Fatalf("并发收集次数上界被破坏: max=%d want<=%d", got, upperBound)
	}
	fmt.Printf("并发压力判定通过：快照数=%d，MaxCollections=%d，上界=%d\n", len(reads), s.MaxCollections(), upperBound)
}
