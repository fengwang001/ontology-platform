package chunker

import (
	"reflect"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func mustNew(t *testing.T, min, max int, window time.Duration, c Clock) *Chunker {
	t.Helper()
	ch, err := New(min, max, window, c)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		min, max    int
		window      time.Duration
		advance     time.Duration // clock step applied between the two feeds
		feedA       int
		feedB       int
		wantSizes   []int
		wantFlush   int
		wantFlushed bool
	}{
		{"split-at-max", 2, 4, time.Hour, 0, 6, 4, []int{4, 4}, 2, true},
		{"min-with-zero-window", 3, 8, 0, 0, 10, 0, []int{3, 3, 3}, 1, true},
		{"aggregate-below-min", 4, 64, time.Second, 0, 2, 1, nil, 3, true},
		{"window-flushes-stragglers", 4, 64, time.Second, 2 * time.Second, 2, 3, []int{4}, 1, true},
		{"empty-flush", 4, 64, time.Second, 0, 0, 0, nil, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{}
			ch := mustNew(t, c.min, c.max, c.window, clock)
			var sizes []int
			sizes = append(sizes, ch.Feed(make([]byte, c.feedA))...)
			clock.add(c.advance)
			sizes = append(sizes, ch.Feed(make([]byte, c.feedB))...)
			if !reflect.DeepEqual(sizes, c.wantSizes) {
				t.Fatalf("sizes = %v, want %v", sizes, c.wantSizes)
			}
			n, ok := ch.Flush()
			if n != c.wantFlush || ok != c.wantFlushed {
				t.Fatalf("flush = %d,%v, want %d,%v", n, ok, c.wantFlush, c.wantFlushed)
			}
		})
	}
}

// The same byte stream must yield the same chunk sequence no matter how
// upstream splits its Feed calls.
func TestCallBoundaryAgnostic(t *testing.T) {
	data := make([]byte, 20)
	want := []int{8, 8}
	for split := 1; split <= len(data); split++ {
		ch := mustNew(t, 3, 8, time.Hour, &fakeClock{})
		var sizes []int
		for i := 0; i < len(data); i += split {
			end := i + split
			if end > len(data) {
				end = len(data)
			}
			sizes = append(sizes, ch.Feed(data[i:end])...)
		}
		if !reflect.DeepEqual(sizes, want) {
			t.Fatalf("split %d: sizes = %v, want %v", split, sizes, want)
		}
		if n, ok := ch.Flush(); !ok || n != 4 {
			t.Fatalf("split %d: flush = %d,%v, want 4,true", split, n, ok)
		}
	}
}

func TestExportImport(t *testing.T) {
	clock := &fakeClock{}
	ch := mustNew(t, 2, 8, time.Hour, clock)
	ch.Feed(make([]byte, 5))
	st := ch.Export()
	if st.Pending != 5 {
		t.Fatalf("pending = %d, want 5", st.Pending)
	}
	other := mustNew(t, 2, 8, time.Hour, clock)
	other.Import(st)
	if got := other.Feed(make([]byte, 3)); !reflect.DeepEqual(got, []int{8}) {
		t.Fatalf("sizes after import = %v, want [8]", got)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(0, 4, 0, &fakeClock{}); err == nil {
		t.Fatal("min=0 accepted")
	}
	if _, err := New(4, 2, 0, &fakeClock{}); err == nil {
		t.Fatal("max<min accepted")
	}
	if _, err := New(1, 4, 0, nil); err == nil {
		t.Fatal("nil clock accepted")
	}
}
