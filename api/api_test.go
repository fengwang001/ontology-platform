package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

func lcgSeq(n int, seed int64) []int64 {
	out := make([]int64, n)
	s := seed
	for i := range out {
		s = s*6364136223846793005 + 1442695040888963407
		out[i] = (s>>33)%201 - 100
	}
	return out
}

func fill(t *testing.T, seq []int64) *api.Index {
	t.Helper()
	x := api.New(len(seq))
	for _, ts := range seq {
		if err := x.Append(ts); err != nil {
			t.Fatalf("Append(%d): %v", ts, err)
		}
	}
	return x
}

// TestForwardExact 钉住不变量 1：TSAt(o) 逐位点等于第 o 次 Append 的 ts。
func TestForwardExact(t *testing.T) {
	cases := [][]int64{
		{2, 1, 8, 3, 4, 9, 5, 7},
		{-7},
		{0, 0, 0},
		{-100, 100, -100, 100},
		lcgSeq(500, 9),
	}
	for _, seq := range cases {
		x := fill(t, seq)
		if x.Len() != len(seq) {
			t.Fatalf("Len=%d want %d", x.Len(), len(seq))
		}
		for o, ts := range seq {
			if got, err := x.TSAt(int64(o)); err != nil || got != ts {
				t.Fatalf("TSAt(%d)=%v,%v want %d", o, got, err, ts)
			}
		}
	}
}

// TestSafeOffMatchesNaive 钉住不变量 2：SafeOff 与朴素线性扫描逐值相同。
func TestSafeOffMatchesNaive(t *testing.T) {
	for _, n := range []int{1, 2, 3, 8, 50, 300} {
		seq := lcgSeq(n, int64(n)*7+3)
		x := fill(t, seq)
		for T := int64(-105); T <= 105; T += 3 {
			want := naive(seq, T)
			got, found, err := x.SafeOff(T)
			if err != nil {
				t.Fatalf("n=%d SafeOff(%d): %v", n, T, err)
			}
			if (want >= 0) != found || (found && got != want) {
				t.Fatalf("n=%d SafeOff(%d)=%d,%v naive=%d", n, T, got, found, want)
			}
		}
	}
}

func naive(seq []int64, T int64) int64 {
	pm, ans := seq[0], int64(-1)
	for o, ts := range seq {
		if ts > pm {
			pm = ts
		}
		if pm <= T {
			ans = int64(o)
		}
	}
	return ans
}

// TestRejectedOpsLeaveState 钉住不变量 4：三类哨兵错误互不相同、被拒后状态不变。
func TestRejectedOpsLeaveState(t *testing.T) {
	if api.ErrOutOfRange == api.ErrEmpty || api.ErrEmpty == api.ErrFull || api.ErrOutOfRange == api.ErrFull {
		t.Fatal("sentinel errors not distinct")
	}
	seq := []int64{2, 1, 8, 3, 4, 9, 5, 7}
	x := fill(t, seq)
	state := func() (int, int64) { // 条数 + 全部记录的哈希指纹
		var h int64
		for i := 0; i < x.Len(); i++ {
			v, err := x.TSAt(int64(i))
			if err != nil {
				t.Fatalf("state TSAt(%d): %v", i, err)
			}
			h = h*31 + v
		}
		return x.Len(), h
	}
	n0, h0 := state()
	if _, err := x.TSAt(-1); !errors.Is(err, api.ErrOutOfRange) {
		t.Fatal("TSAt(-1) not ErrOutOfRange")
	}
	if _, err := x.TSAt(int64(len(seq))); !errors.Is(err, api.ErrOutOfRange) {
		t.Fatal("TSAt(N) not ErrOutOfRange")
	}
	if err := x.Append(1); !errors.Is(err, api.ErrFull) {
		t.Fatal("overflow Append not ErrFull")
	}
	if _, _, err := api.New(2).SafeOff(0); !errors.Is(err, api.ErrEmpty) {
		t.Fatal("empty SafeOff not ErrEmpty")
	}
	if n1, h1 := state(); n1 != n0 || h1 != h0 {
		t.Fatal("state changed by rejected ops")
	}
	if o, f, err := x.SafeOff(7); err != nil || !f || o != 1 { // 仍可正常使用
		t.Fatalf("SafeOff(7) after rejects = %d,%v,%v", o, f, err)
	}
}

// TestConcurrentReaders 并发只读同一实例，结果逐值相同；不用 sleep。
func TestConcurrentReaders(t *testing.T) {
	seq := lcgSeq(2000, 5)
	x := fill(t, seq)
	wantSafe, wantFound, err := x.SafeOff(50)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for o := g; o < len(seq); o += 32 {
				if got, err := x.TSAt(int64(o)); err != nil || got != seq[o] {
					t.Errorf("TSAt(%d)=%d,%v", o, got, err)
				}
			}
			if so, f, err := x.SafeOff(50); err != nil || f != wantFound || so != wantSafe || x.Len() != len(seq) {
				t.Errorf("SafeOff=%d,%v,%v", so, f, err)
			}
		}(g)
	}
	wg.Wait()
}
