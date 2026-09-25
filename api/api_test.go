package api

import (
	"errors"
	"fmt"
	"testing"
)

// 不变量 1：与朴素参照一致（重放 ver<=snap 的写，后写覆盖先写）。
func TestReadMatchesNaiveReplay(t *testing.T) {
	keys := []string{"k0", "k1", "k2", "k3", "k4"}
	for _, n := range []int{50, 500, 5000} {
		st, _ := New(64)
		var logK []string
		var logV []int64 // 第 i 项即 ver=i+1 的写
		var snaps []int
		seed := uint64(n)
		for i := 0; i < n; i++ {
			seed = seed*6364136223846793005 + 1 // LCG，确定性伪随机
			if seed>>62 < 3 {
				k := keys[seed%5]
				st.Write(k, int64(i))
				logK, logV = append(logK, k), append(logV, int64(i))
			} else {
				s, _ := st.Snapshot()
				snaps = append(snaps, s)
			}
		}
		for _, s := range snaps {
			for _, k := range keys {
				var want int64
				for v, k2 := range logK { // 朴素重放
					if v+1 <= s && k2 == k {
						want = logV[v]
					}
				}
				if got, err := st.Read(s, k); err != nil || got != want {
					t.Fatalf("n=%d Read(%d,%s)=%d,%v want %d", n, s, k, got, err, want)
				}
			}
		}
	}
}

// 不变量 2：快照隔离，跨键一致（第三节八步序列）。
func TestSnapshotIsolationCrossKey(t *testing.T) {
	st, _ := New(4)
	st.Write("a", 10)
	st.Write("b", 20)
	s1, _ := st.Snapshot()
	st.Write("a", 15)
	st.Write("b", 25)
	for k, want := range map[string]int64{"a": 10, "b": 20, "c": 0} {
		if got, err := st.Read(s1, k); err != nil || got != want {
			t.Fatalf("Read(s1,%s)=%d,%v want %d", k, got, err, want)
		}
	}
	if st.ReadCurrent("a") != 15 || st.ReadCurrent("b") != 25 {
		t.Fatal("ReadCurrent 应见最新值")
	}
	if err := st.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 不变量 3：Read/Snapshot/ReadCurrent 不改变 ver 与已写值。
func TestReadSnapshotNoSideEffect(t *testing.T) {
	st, _ := New(16)
	st.Write("a", 1)
	s0, _ := st.Snapshot()
	for i := 0; i < 100; i++ {
		st.Read(s0, "a")
		st.ReadCurrent("a")
		s, _ := st.Snapshot()
		st.Release(s)
		if s != s0 {
			t.Fatalf("读操作改变了 ver: %d != %d", s, s0)
		}
	}
	if got, _ := st.Read(s0, "a"); got != 1 {
		t.Fatalf("已写值被改变: %d", got)
	}
}

// 不变量 4 + 故障注入：四类可判定错误互不相同，被拒后状态不变。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	st, _ := New(2)
	st.Write("a", 1)
	st.Write("b", 2) // ver=2，令 s0=2，腾出未登记的合法 id 1
	s0, _ := st.Snapshot()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"非正max", func() error { _, e := New(0); return e }, ErrNonPositiveMax},
		{"空Key写", func() error { return st.Write("", 1) }, ErrEmptyKey},
		{"空Key读", func() error { _, e := st.Read(s0, ""); return e }, ErrEmptyKey},
		{"负快照读", func() error { _, e := st.Read(-1, "a"); return e }, ErrInvalidSnapshot},
		{"超界快照读", func() error { _, e := st.Read(3, "a"); return e }, ErrInvalidSnapshot},
		{"释放未登记", func() error { return st.Release(1) }, ErrInvalidSnapshot},
		{"释放负快照", func() error { return st.Release(-1) }, ErrInvalidSnapshot},
		{"占满名额", func() error { _, e := st.Snapshot(); return e }, nil},
		{"快照超限", func() error { _, e := st.Snapshot(); return e }, ErrSnapshotLimit},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		seen[c.want] = true
	}
	if len(seen) != 5 { // 四个哨兵互不相同（外加 nil 成功项）
		t.Fatal("哨兵错误不互异")
	}
	if got, _ := st.Read(s0, "a"); got != 1 { // 状态不变且可继续用
		t.Fatalf("被拒操作留下痕迹: %d", got)
	}
	if err := st.Release(s0); err != nil {
		t.Fatal("释放存活快照应成功")
	} else if _, err := st.Snapshot(); err != nil { // 释放后名额恢复
		t.Fatalf("释放后仍超限: %v", err)
	}
}

// 并发：快照后 N 个 goroutine 各写不同 Key；快照内全 0，Current 各异。
func TestConcurrentWriteSnapshotRead(t *testing.T) {
	st, _ := New(4)
	s, _ := st.Snapshot()
	const N = 128
	done := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			st.Write(fmt.Sprintf("k%d", i), int64(i+1))
		}(i)
	}
	for i := 0; i < N; i++ {
		<-done
	}
	for i := 0; i < N; i++ {
		k := fmt.Sprintf("k%d", i)
		if v, _ := st.Read(s, k); v != 0 {
			t.Fatalf("快照读到后写值: %s=%d", k, v)
		}
		if v := st.ReadCurrent(k); v != int64(i+1) {
			t.Fatalf("Current(%s)=%d want %d", k, v, i+1)
		}
	}
}
