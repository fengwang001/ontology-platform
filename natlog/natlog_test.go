package natlog

import (
	"errors"
	"math"
	"testing"
)

func TestLogAppendLookup(t *testing.T) {
	lg := New()
	lg.Append(Alloc, 7, 0, 1024, 1039, 0)
	lg.Append(Alloc, 9, 1, 1024, 1039, 1)
	lg.Append(Free, 7, 0, 1024, 1039, 150)

	cases := []struct {
		name   string
		addr   int
		port   int
		at     int64
		want   int64
		wantEr error
	}{
		{"alloc 左闭", 0, 1024, 0, 7, nil},
		{"free 前一刻仍持有", 0, 1039, 149, 7, nil},
		{"free 右开", 0, 1024, 150, 0, ErrUnallocated},
		{"alloc 前未分配", 1, 1024, 0, 0, ErrUnallocated},
		{"alloc 左闭-另一址", 1, 1030, 1, 9, nil},
		{"t 超最大 now", 0, 1024, 151, 0, ErrInvalidArgument},
		{"非法负 t", 0, 1024, -1, 0, ErrInvalidArgument},
		{"同块重分配左闭", 0, 1040, 150, 0, ErrUnallocated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := lg.Lookup(c.addr, c.port, c.at)
			if !errors.Is(err, c.wantEr) {
				t.Fatalf("err=%v want %v", err, c.wantEr)
			}
			if got != c.want {
				t.Fatalf("sub=%d want %d", got, c.want)
			}
		})
	}

	// 重分配后 t=150 归新分配。
	lg.Append(Alloc, 7, 0, 1024, 1039, 150)
	if sub, err := lg.Lookup(0, 1024, 150); err != nil || sub != 7 {
		t.Fatalf("realloc lookup = %d,%v", sub, err)
	}
	es := lg.Entries()
	for i := range es {
		if es[i].Seq != int64(i+1) {
			t.Fatalf("seq not continuous")
		}
	}
}

func TestLookupCompareBound(t *testing.T) {
	lg := New()
	// 构造一块的 1000 次 ALLOC/FREE 历史。
	n := 1000
	for i := 0; i < n; i++ {
		base := int64(2 * i)
		lg.Append(Alloc, int64(i+1), 3, 2000, 2015, base)
		lg.Append(Free, int64(i+1), 3, 2000, 2015, base+1)
	}
	for _, at := range []int64{1, 500, 999, 1998} {
		if _, err := lg.Lookup(3, 2010, at); err != nil && !errors.Is(err, ErrUnallocated) {
			t.Fatalf("at=%d: %v", at, err)
		}
		// 该块 ALLOC 历史条数（最后一次未 FREE，故归属链 n 条）。
		if cmps := lg.LastLookupCmps(); cmps > int(math.Log2(float64(n)))+2 {
			t.Fatalf("cmps=%d exceeds log2(%d)+2", cmps, n)
		}
	}
}
