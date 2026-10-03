package cal

import (
	"math/rand"
	"testing"
)

type naiveCal struct {
	open, close int64
	mask        uint8
	holidays    map[int64]bool
}

func (n *naiveCal) workMinute(t int64) bool {
	day := t / 1440
	rem := t % 1440
	if n.holidays[day] || n.mask&(1<<uint(day%7)) == 0 {
		return false
	}
	return rem >= n.open && rem < n.close
}

func (n *naiveCal) work(t1, t2 int64) int64 {
	var cnt int64
	for t := t1; t < t2; t++ {
		if n.workMinute(t) {
			cnt++
		}
	}
	return cnt
}

func (n *naiveCal) advance(t, need int64) (int64, bool) {
	if need == 0 {
		for t <= MaxTime && !n.workMinute(t) {
			t++
		}
		return t, t <= MaxTime
	}
	for need > 0 {
		if t > MaxTime {
			return 0, false
		}
		if n.workMinute(t) {
			need--
		}
		t++
	}
	return t, true
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 200; iter++ {
		open := rng.Int63n(1000)
		cl := open + 1 + rng.Int63n(1440-open)
		mask := uint8(1 + rng.Intn(127))
		c, err := New(open, cl, mask)
		if err != nil {
			t.Fatalf("iter %d New(%d,%d,%d): %v", iter, open, cl, mask, err)
		}
		nc := &naiveCal{open: open, close: cl, mask: mask, holidays: map[int64]bool{}}
		nh := rng.Intn(12)
		now := int64(0)
		for h := 0; h < nh; h++ {
			now += rng.Int63n(3 * 1440)
			day := now/1440 + 1 + rng.Int63n(20)
			if err := c.AddHoliday(day, now); err != nil {
				t.Fatalf("iter %d AddHoliday: %v", iter, err)
			}
			nc.holidays[day] = true
		}
		limit := int64(40 * 1440)
		for k := 0; k < 30; k++ {
			t1 := rng.Int63n(limit)
			t2 := t1 + rng.Int63n(limit-t1)
			got, err := c.Work(t1, t2)
			if err != nil {
				t.Fatalf("iter %d Work: %v", iter, err)
			}
			if want := nc.work(t1, t2); got != want {
				t.Fatalf("iter %d Work(%d,%d)=%d want %d (open=%d close=%d mask=%b holidays=%v)",
					iter, t1, t2, got, want, open, cl, mask, nc.holidays)
			}
			if rng.Intn(2) == 0 {
				need := rng.Int63n(3000)
				gotAt, gotOK := c.Advance(t1, need)
				wantAt, wantOK := nc.advance(t1, need)
				if gotOK != wantOK || (gotOK && gotAt != wantAt) {
					t.Fatalf("iter %d Advance(%d,%d)=(%d,%v) want (%d,%v)",
						iter, t1, need, gotAt, gotOK, wantAt, wantOK)
				}
			}
		}
	}
}
