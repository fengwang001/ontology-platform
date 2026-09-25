package snap

import (
	"errors"
	"fmt"
	"math/bits"
	"sync"
	"testing"
)

// 钉住复杂度约束：Read 在版本历史里二分定位，检查条目数不随 m 线性增长。
func TestReadChecksSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		st, err := New(1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if err := st.Write("k", int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		for _, snap := range []int{1, m / 2, m} {
			if _, err := st.Read(snap, "k"); err != nil {
				t.Fatal(err)
			}
			got := st.checked.Load()
			limit := int64(bits.Len(uint(m))) + 1
			if got > limit {
				t.Fatalf("m=%d snap=%d: checked %d entries, want <= %d (O(log m))", m, snap, got, limit)
			}
			if got > int64(m)/2 {
				t.Fatalf("m=%d snap=%d: checked %d entries grows linearly", m, snap, got)
			}
		}
	}
}

// 钉住二分定位的正确性：每个快照版本都取到 ver<=snap 的最大版本。
func TestReadBinarySearchCorrect(t *testing.T) {
	for _, m := range []int{1, 2, 3, 100, 1000} {
		st, _ := New(1)
		for i := 0; i < m; i++ {
			if err := st.Write("k", int64(i*10)); err != nil {
				t.Fatal(err)
			}
		}
		for snap := 0; snap <= m; snap++ {
			got, err := st.Read(snap, "k")
			if err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if snap >= 1 {
				want = int64((snap - 1) * 10)
			}
			if got != want {
				t.Fatalf("m=%d snap=%d: got %d want %d", m, snap, got, want)
			}
		}
	}
}

// 四类故障注入的哨兵错误互不相同，且各自可被真实触发。
func TestErrorsDistinct(t *testing.T) {
	errs := []error{ErrMaxSnapshots, ErrEmptyKey, ErrInvalidSnapshot, ErrTooManySnapshots, ErrSnapshotNotFound}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("errors %d,%d not distinct: %v vs %v", i, j, errs[i], errs[j])
			}
		}
	}
	if _, err := New(0); !errors.Is(err, ErrMaxSnapshots) {
		t.Fatalf("New(0): got %v", err)
	}
	st, _ := New(1)
	_, _ = st.Snapshot()
	if _, err := st.Snapshot(); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("overflow: got %v", err)
	}
	if err := st.Release(0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Snapshot(); err != nil {
		t.Fatalf("unusable after release: %v", err)
	}
}

// 并发：快照后 N 个 goroutine 各写一个 Key，快照读全为 0，当前读为各自值。
func TestConcurrentWritesSnapshotReads(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		st, _ := New(4)
		sn, _ := st.Snapshot()
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = st.Write(fmt.Sprintf("k%d", i), int64(i+1))
			}(i)
		}
		wg.Wait()
		for i := 0; i < n; i++ {
			k := fmt.Sprintf("k%d", i)
			if v, err := st.Read(sn, k); err != nil || v != 0 {
				t.Fatalf("n=%d key=%s: snapshot read got %v,%v want 0", n, k, v, err)
			}
			if v := st.ReadCurrent(k); v != int64(i+1) {
				t.Fatalf("n=%d key=%s: current got %d want %d", n, k, v, i+1)
			}
		}
	}
}
