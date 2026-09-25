package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type lcg struct{ s uint64 }

func (l *lcg) next(n int) int {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	return int(l.s>>33) % n
}

// 不变量 1：Read 等于「重放 ver<=snap 的写、后写覆盖先写」的朴素结果。
func TestReadMatchesNaiveReplay(t *testing.T) {
	for _, tc := range [][3]int{{7, 5, 200}, {99, 9, 500}, {123456, 3, 1000}} {
		seed, nk, ops := uint64(tc[0]), tc[1], tc[2]
		st, _ := New(1 << 20)
		r := &lcg{seed}
		hist := map[string][][2]int64{}
		var snaps []int
		for i := 0; i < ops; i++ {
			k, v := fmt.Sprintf("k%d", r.next(nk)), int64(r.next(10000))
			sn, _ := st.Snapshot()
			if err := st.Write(k, v); err != nil {
				t.Fatal(err)
			}
			hist[k] = append(hist[k], [2]int64{int64(sn + 1), v})
			if i%13 == 0 {
				snaps = append(snaps, sn)
			}
		}
		for _, sn := range snaps {
			for k, hs := range hist {
				var want int64
				for _, e := range hs {
					if e[0] <= int64(sn) {
						want = e[1]
					}
				}
				if got, err := st.Read(sn, k); err != nil || got != want {
					t.Fatalf("seed=%d snap=%d key=%s: got %v,%v want %v", seed, sn, k, got, err, want)
				}
			}
		}
	}
}

// 不变量 2：同一快照跨键一致，快照之后的写不影响该快照；并钉住 NOTES.md 八步推导。
func TestSnapshotIsolationCrossKey(t *testing.T) {
	for _, n := range []int{2, 8, 32} {
		st, _ := New(4)
		key := func(i int) string { return fmt.Sprintf("k%d", i) }
		for i := 0; i < n; i++ {
			_ = st.Write(key(i), int64(i+1))
		}
		sn, _ := st.Snapshot()
		for i := 0; i < n; i++ {
			_ = st.Write(key(i), int64(1000+i))
		}
		for i := 0; i < n; i++ {
			got, err := st.Read(sn, key(i))
			if err != nil || got != int64(i+1) || st.ReadCurrent(key(i)) != int64(1000+i) {
				t.Fatalf("n=%d i=%d: got %v,%v", n, i, got, err)
			}
		}
	}
	// NOTES.md 第三节八步序列：s1=2，s2=4，第 5 步读 a=10，第 7 步读 b=20。
	st, _ := New(8)
	_ = st.Write("a", 10)
	_ = st.Write("b", 20)
	s1, _ := st.Snapshot()
	_ = st.Write("a", 15)
	r5, _ := st.Read(s1, "a")
	_ = st.Write("b", 25)
	r7, _ := st.Read(s1, "b")
	s2, _ := st.Snapshot()
	if s1 != 2 || s2 != 4 || r5 != 10 || r7 != 20 {
		t.Fatalf("s1=%d s2=%d r5=%d r7=%d, want 2 4 10 20", s1, s2, r5, r7)
	}
}

// 不变量 3：Read/Snapshot/ReadCurrent 不改变 ver 与已写值。
func TestReadsDoNotMutate(t *testing.T) {
	for _, ops := range []int{10, 100} {
		st, _ := New(1 << 20)
		_ = st.Write("a", 1)
		_ = st.Write("b", 2)
		before, _ := st.Snapshot()
		for i := 0; i < ops; i++ {
			_, _ = st.Read(before, "a")
			_, _ = st.Read(0, "b")
			_ = st.ReadCurrent("a")
			_, _ = st.Snapshot()
		}
		after, _ := st.Snapshot()
		if before != after || st.ReadCurrent("a") != 1 || st.ReadCurrent("b") != 2 {
			t.Fatalf("ops=%d: reads mutated state", ops)
		}
	}
}

// 不变量 4：被拒操作不改变任何状态，拒绝后仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(st *Store) error
		want error
	}{
		{"empty-key", func(st *Store) error { return st.Write("", 1) }, ErrEmptyKey},
		{"read-negative-snap", func(st *Store) error { _, e := st.Read(-1, "a"); return e }, ErrInvalidSnapshot},
		{"read-future-snap", func(st *Store) error { _, e := st.Read(3, "a"); return e }, ErrInvalidSnapshot},
		{"release-negative", func(st *Store) error { return st.Release(-1) }, ErrInvalidSnapshot},
		{"release-unregistered", func(st *Store) error { return st.Release(0) }, ErrSnapshotNotFound},
	}
	for _, tc := range cases {
		st, _ := New(4)
		_ = st.Write("a", 10)
		sn, _ := st.Snapshot()
		before, _ := st.Snapshot()
		if err := tc.op(st); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		after, _ := st.Snapshot()
		v, _ := st.Read(sn, "a")
		if before != after || v != 10 || st.ReadCurrent("a") != 10 {
			t.Fatalf("%s: state mutated by rejected op", tc.name)
		}
		if err := st.Write("b", 20); err != nil {
			t.Fatalf("%s: unusable after rejection: %v", tc.name, err)
		}
	}
}

// SelfCheck 可并发调用且结果正确。
func TestSelfCheck(t *testing.T) {
	st, _ := New(8)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = st.SelfCheck() }()
	}
	wg.Wait()
	if err := st.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
