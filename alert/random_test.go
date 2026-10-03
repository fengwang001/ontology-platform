package alert

import (
	"math/rand"
	"testing"

	"ontology/cal"
	"ontology/sla"
)

type refTimer struct {
	budget  int64
	running bool
	segs    [][2]int64
	elapsed map[int64]int64
	trig    map[uint8]int64
	due     map[uint8]bool
	acked   map[uint8]bool
}

type refWorld struct {
	open, close int64
	mask        uint8
	holidays    map[int64]bool
	timers      map[string]*refTimer
	clock       int64
}

func (w *refWorld) workMin(t int64) bool {
	d := t / 1440
	r := t % 1440
	return !w.holidays[d] && w.mask&(1<<uint(d%7)) != 0 && r >= w.open && r < w.close
}

func (w *refWorld) elapsed(rt *refTimer, now int64) int64 {
	if v, ok := rt.elapsed[now]; ok {
		return v
	}
	var total int64
	for _, seg := range rt.segs {
		start, end := seg[0], seg[1]
		if start >= now {
			continue
		}
		if end == 0 || end > now {
			end = now
		}
		for t := start; t < end; t++ {
			if w.workMin(t) {
				total++
			}
		}
	}
	rt.elapsed[now] = total
	return total
}

func thresh(b int64, q uint8) int64 { return (b*int64(q) + 99) / 100 }

func (w *refWorld) recompute(rt *refTimer, now int64) {
	for _, q := range Levels {
		target := thresh(rt.budget, q)
		if w.elapsed(rt, now) >= target {
			lo, hi := int64(0), now+1
			for lo < hi {
				mid := (lo + hi) / 2
				if w.elapsed(rt, mid) >= target {
					hi = mid
				} else {
					lo = mid + 1
				}
			}
			if !rt.due[q] || rt.trig[q] != lo {
				rt.trig[q], rt.due[q] = lo, true
			}
		}
	}
}

func (w *refWorld) tick(now int64) []Alert {
	var out []Alert
	for id, rt := range w.timers {
		w.recompute(rt, now)
		for _, q := range Levels {
			if rt.due[q] && rt.trig[q] <= now && !rt.acked[q] {
				out = append(out, Alert{[]byte(id), q, rt.trig[q]})
			}
		}
	}
	sortAlerts(out)
	return out
}

func sortAlerts(a []Alert) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0; j-- {
			x, y := a[j-1], a[j]
			less := x.At < y.At || (x.At == y.At && (string(x.ID) < string(y.ID) ||
				(string(x.ID) == string(y.ID) && x.Level < y.Level)))
			if !less {
				a[j-1], a[j] = y, x
			}
		}
	}
}

func TestRandomSequenceVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for iter := 0; iter < 60; iter++ {
		open := rng.Int63n(800)
		cl := open + 1 + rng.Int63n(1440-open)
		mask := uint8(1 + rng.Intn(127))
		c, err := cal.New(open, cl, mask)
		if err != nil {
			t.Fatal(err)
		}
		sm := sla.NewManager(c)
		am := NewManager(sm, c)
		w := &refWorld{
			open: open, close: cl, mask: mask,
			holidays: map[int64]bool{},
			timers:   map[string]*refTimer{},
		}
		now := int64(0)
		ids := []string{"alpha", "beta", "k"}
		const horizon = 12 * 1440
		for step := 0; step < 120; step++ {
			now += int64(rng.Intn(400))
			id := ids[rng.Intn(len(ids))]
			rt, exists := w.timers[id]
			switch rng.Intn(6) {
			case 0:
				if !exists {
					b := int64(1 + rng.Intn(1500))
					if err := sm.Start([]byte(id), b, now); err != nil {
						t.Fatalf("iter %d step %d Start: %v", iter, step, err)
					}
					w.timers[id] = &refTimer{
						budget: b, running: true,
						segs:    [][2]int64{{now, 0}},
						elapsed: map[int64]int64{},
						trig:    map[uint8]int64{}, due: map[uint8]bool{},
						acked: map[uint8]bool{},
					}
				}
			case 1:
				if exists && rt.running {
					if err := sm.Pause([]byte(id), now); err != nil {
						t.Fatalf("Pause: %v", err)
					}
					rt.segs[len(rt.segs)-1][1] = now
					rt.running = false
				}
			case 2:
				if exists && !rt.running {
					if err := sm.Resume([]byte(id), now); err != nil {
						t.Fatalf("Resume: %v", err)
					}
					rt.segs = append(rt.segs, [2]int64{now, 0})
					rt.running = true
				}
			case 3:
				day := now/1440 + 1 + int64(rng.Intn(14))
				e1 := c.AddHoliday(day, now)
				e2 := w.addHoliday(day, now)
				if (e1 == nil) != (e2 == nil) {
					t.Fatalf("iter %d AddHoliday(%d,%d): real=%v ref=%v", iter, day, now, e1, e2)
				}
			case 4:
				if exists {
					q := Levels[rng.Intn(len(Levels))]
					errReal := am.Ack([]byte(id), int64(q), now)
					errRef := w.ack(rt, q, now)
					if (errReal == nil) != (errRef == nil) {
						t.Fatalf("iter %d Ack(%s,%d,%d): real=%v ref=%v",
							iter, id, q, now, errReal, errRef)
					}
				}
			case 5:
				got, err := am.Tick(now)
				if err != nil {
					t.Fatalf("Tick: %v", err)
				}
				want := w.tick(now)
				if !alertsEqual(got, want) {
					t.Fatalf("iter %d step %d Tick(%d):\n got %+v\nwant %+v",
						iter, step, now, got, want)
				}
			}
			if now > horizon*3 {
				break
			}
		}
	}
}
