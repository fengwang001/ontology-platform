package natlog

import (
	"errors"
	"math/bits"
	"testing"
)

func TestEntriesSeqAndFields(t *testing.T) {
	l := NewLog(2, 1024, 1087, 16)
	l.Observe(7)
	l.Append(Alloc, 7, 0, 1024, 1039, 3)
	l.Append(Free, 7, 0, 1024, 1039, 5)
	es := l.Entries()
	if len(es) != 2 {
		t.Fatalf("want 2 entries, got %d", len(es))
	}
	want := []Entry{
		{Seq: 1, Kind: Alloc, Sub: 7, Addr: 0, First: 1024, Last: 1039, Time: 3},
		{Seq: 2, Kind: Free, Sub: 7, Addr: 0, First: 1024, Last: 1039, Time: 5},
	}
	for i, e := range es {
		if e != want[i] {
			t.Fatalf("entry %d: got %+v want %+v", i, e, want[i])
		}
	}
	if es[0].Kind.String() != "ALLOC" || es[1].Kind.String() != "FREE" {
		t.Fatalf("kind strings: %v %v", es[0].Kind, es[1].Kind)
	}
}

func TestLookupValidation(t *testing.T) {
	l := NewLog(2, 1024, 1087, 16)
	if _, err := l.Lookup(0, 1024, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("lookup before any accepted op: got %v", err)
	}
	l.Observe(100)
	cases := []struct {
		name       string
		addr, port int
		tm         int64
	}{
		{"addr negative", -1, 1024, 0},
		{"addr too large", 2, 1024, 0},
		{"port below range", 0, 1023, 0},
		{"port above range", 0, 1088, 0},
		{"negative time", 0, 1024, -1},
		{"time beyond max now", 0, 1024, 101},
		{"time beyond 1e12", 0, 1024, 1_000_000_000_001},
	}
	for _, c := range cases {
		if _, err := l.Lookup(c.addr, c.port, c.tm); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s: got %v, want ErrInvalidParam", c.name, err)
		}
	}
	if _, err := l.Lookup(0, 1024, 100); !errors.Is(err, ErrNotAllocated) {
		t.Fatalf("empty log at max now: got %v, want ErrNotAllocated", err)
	}
}

func TestLookupBinarySearchAndComparisons(t *testing.T) {
	l := NewLog(1, 1024, 1055, 16)
	// 40 allocation cycles on block 0: ALLOC@(2i), FREE@(2i+1).
	const cycles = 40
	for i := 0; i < cycles; i++ {
		l.Append(Alloc, 100+i, 0, 1024, 1039, int64(2*i))
		l.Append(Free, 100+i, 0, 1024, 1039, int64(2*i+1))
	}
	l.Observe(2 * cycles)
	hist := 2 * cycles
	maxCmps := bits.Len(uint(hist)) + 1 // floor(log2(hist)) + 2
	for tm := int64(0); tm <= 2*cycles; tm++ {
		sub, err := l.Lookup(0, 1024, tm)
		if l.lastCmps > maxCmps {
			t.Fatalf("t=%d: %d comparisons, bound %d", tm, l.lastCmps, maxCmps)
		}
		if tm%2 == 0 && tm < 2*cycles {
			want := 100 + int(tm/2)
			if err != nil || sub != want {
				t.Fatalf("t=%d: got (%d,%v), want (%d,nil)", tm, sub, err, want)
			}
		} else {
			if !errors.Is(err, ErrNotAllocated) {
				t.Fatalf("t=%d: got (%d,%v), want ErrNotAllocated", tm, sub, err)
			}
		}
	}
	// A port of a block that never saw an allocation.
	if _, err := l.Lookup(0, 1040, 0); !errors.Is(err, ErrNotAllocated) {
		t.Fatalf("wrong block mapped: got %v", err)
	}
	// Port 1030 belongs to block 0 (S=16): same history must answer.
	if _, err := l.Lookup(0, 1030, 2*cycles-1); !errors.Is(err, ErrNotAllocated) {
		t.Fatalf("block mapping: got %v, want ErrNotAllocated", err)
	}
}

func TestLookupSameTimestampAllocFree(t *testing.T) {
	l := NewLog(1, 1024, 1039, 16)
	l.Append(Alloc, 7, 0, 1024, 1039, 5)
	l.Append(Free, 7, 0, 1024, 1039, 5)
	l.Append(Alloc, 9, 0, 1024, 1039, 5)
	l.Observe(5)
	// At t=5 the first allocation is already freed; the second holds the block.
	if sub, err := l.Lookup(0, 1024, 5); err != nil || sub != 9 {
		t.Fatalf("t=5: got (%d,%v), want (9,nil)", sub, err)
	}
	if _, err := l.Lookup(0, 1024, 4); !errors.Is(err, ErrNotAllocated) {
		t.Fatalf("t=4: got %v, want ErrNotAllocated", err)
	}
}
