package chunker

import (
	"testing"
	"time"
)

func collect(chunks []Chunk, sizes *[]int) {
	for _, ch := range chunks {
		*sizes = append(*sizes, len(ch.Data))
	}
}

func newTestChunker(t *testing.T, clk *ManualClock) *Chunker {
	t.Helper()
	c, err := New(Config{MinChunk: 4, MaxChunk: 8, Window: 10 * time.Second, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// 第 6 条：同一字节流在任意 Write 调用切分下，块序列完全相同。
func TestChunkSequenceBoundaryIndependent(t *testing.T) {
	data := make([]byte, 37)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	splits := [][]int{
		{37},
		{1, 1, 35},
		{7, 8, 9, 13},
		{8, 8, 8, 8, 5},
	}
	var want []int
	for si, bounds := range splits {
		clk := NewManualClock(time.Unix(1, 0))
		c := newTestChunker(t, clk)
		var sizes []int
		pos := 0
		for _, n := range bounds {
			chs, err := c.Write(data[pos : pos+n])
			if err != nil {
				t.Fatal(err)
			}
			pos += n
			collect(chs, &sizes)
		}
		collect(c.Close(), &sizes)
		if si == 0 {
			want = sizes
			continue
		}
		if len(sizes) != len(want) {
			t.Fatalf("split %d sizes=%v want=%v", si, sizes, want)
		}
		for i := range want {
			if sizes[i] != want[i] {
				t.Fatalf("split %d sizes=%v want=%v", si, sizes, want)
			}
		}
	}
}

func TestMaxChunkSplitAndZeroWrite(t *testing.T) {
	cases := []struct {
		name    string
		write   int
		wantOut []int
		pending int
	}{
		{"small aggregates", 3, nil, 3},
		{"exact full chunk", 8, []int{8}, 0},
		{"large write splits", 19, []int{8, 8}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := NewManualClock(time.Unix(1, 0))
			c := newTestChunker(t, clk)
			chs, err := c.Write(make([]byte, tc.write))
			if err != nil {
				t.Fatal(err)
			}
			var got []int
			collect(chs, &got)
			if eqInts(got, tc.wantOut) != true || len(c.pending) != tc.pending {
				t.Fatalf("out=%v pending=%d want %v/%d", got, len(c.pending), tc.wantOut, tc.pending)
			}
			// 第 1 条：任意次数的零长写入不产生任何块、不改聚合状态。
			p0 := c.Save()
			for range 100 {
				chs, err := c.Write(nil)
				if err != nil || chs != nil {
					t.Fatalf("zero write produced %v err=%v", chs, err)
				}
			}
			if !stateEqual(c.Save(), p0) {
				t.Fatal("zero write changed chunker state")
			}
		})
	}
}

func TestWindowAggregation(t *testing.T) {
	cases := []struct {
		name    string
		writes  []int
		advance time.Duration
		want    []int // Tick 吐出的块大小
		left    int
	}{
		{"window not expired", []int{2, 2}, 9 * time.Second, nil, 4},
		{"window expired aggregates", []int{2, 2}, 10 * time.Second, []int{4}, 0},
		{"below min keeps waiting", []int{2}, 10 * time.Second, nil, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := NewManualClock(time.Unix(1, 0))
			c := newTestChunker(t, clk)
			for _, n := range tc.writes {
				if _, err := c.Write(make([]byte, n)); err != nil {
					t.Fatal(err)
				}
			}
			clk.Advance(tc.advance)
			var got []int
			collect(c.Tick(), &got)
			if !eqInts(got, tc.want) || len(c.pending) != tc.left {
				t.Fatalf("tick out=%v pending=%d want %v/%d", got, len(c.pending), tc.want, tc.left)
			}
			// Close 幂等，尾部最终仍会被吐出。
			tail := c.Close()
			if c.Close() != nil {
				t.Fatal("Close not idempotent")
			}
			if tc.left == 0 && len(tail) != 0 {
				t.Fatalf("unexpected tail %v", tail)
			}
		})
	}
}

func TestWriteAfterClose(t *testing.T) {
	c := newTestChunker(t, NewManualClock(time.Unix(1, 0)))
	c.Close()
	if _, err := c.Write([]byte("x")); err != ErrClosed {
		t.Fatalf("err=%v want ErrClosed", err)
	}
}

func TestSaveRestore(t *testing.T) {
	clk := NewManualClock(time.Unix(1, 0))
	c := newTestChunker(t, clk)
	if _, err := c.Write(make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	s := c.Save()
	if _, err := c.Write(make([]byte, 9)); err != nil {
		t.Fatal(err)
	}
	c.Restore(s)
	if len(c.pending) != 5 || c.closed {
		t.Fatalf("restore failed: pending=%d closed=%v", len(c.pending), c.closed)
	}
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stateEqual(a, b State) bool {
	return bytesEqual(a.Pending, b.Pending) &&
		a.WindowStart.Equal(b.WindowStart) &&
		a.HasWindow == b.HasWindow && a.Closed == b.Closed
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
