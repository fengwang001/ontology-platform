package alert

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"ontology/cal"
	"ontology/sla"
)

// 朴素模型：逐分钟模拟工作状态，触发器按段累计扫描。
type nseg struct{ start, end int64 }

type ntimer struct {
	budget int64
	segs   []nseg
	paused bool
	exists bool
	ack    map[int]bool
}

type naive struct {
	open, closeV int64
	mask         uint8
	hol          map[int64]bool
	timers       map[string]*ntimer
	clock        int64
}

func newNaive(o, c int64, m uint8) *naive {
	return &naive{open: o, closeV: c, mask: m, hol: map[int64]bool{},
		timers: map[string]*ntimer{}}
}

func (n *naive) work(t int64) bool {
	d := t / 1440
	x := t - d*1440
	return n.mask>>(d%7)&1 == 1 && !n.hol[d] && x >= n.open && x < n.closeV
}

func (n *naive) elapsed(tm *ntimer, now int64) int64 {
	var sum int64
	for i, s := range tm.segs {
		end := s.end
		if i == len(tm.segs)-1 && !tm.paused {
			end = now
		}
		for t := s.start; t < end; t++ {
			if n.work(t) {
				sum++
			}
		}
	}
	return sum
}

// trigger 返回历史段内恰好累计到 need 的时刻；未在 now 前达到返回 false。
func (n *naive) trigger(tm *ntimer, need, now int64) (int64, bool) {
	var acc int64
	for i, s := range tm.segs {
		end := s.end
		running := i == len(tm.segs)-1 && !tm.paused
		if running {
			if n.elapsed(tm, now) < need {
				return 0, false
			}
			end = now
		}
		for t := s.start; t < end; t++ {
			if n.work(t) {
				acc++
				if acc == need {
					return t + 1, true
				}
			}
		}
	}
	return 0, false
}

func threshold(budget int64, q int) int64 { return (budget*int64(q) + 99) / 100 }

func (n *naive) tick(now int64) []Alarm {
	var out []Alarm
	for id, tm := range n.timers {
		for _, q := range []int{50, 80, 100} {
			if tm.ack[q] {
				continue
			}
			if t, ok := n.trigger(tm, threshold(tm.budget, q), now); ok {
				out = append(out, Alarm{ID: []byte(id), Q: q, T: t})
			}
		}
	}
	sortAlarms(out)
	return out
}

func catErr(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, cal.ErrInvalid):
		return "invalid"
	case errors.Is(err, cal.ErrClock):
		return "clock"
	case errors.Is(err, sla.ErrNotFound):
		return "notfound"
	case errors.Is(err, sla.ErrExists):
		return "exists"
	case errors.Is(err, sla.ErrState):
		return "state"
	case errors.Is(err, sla.ErrInvalid):
		return "invalid"
	case errors.Is(err, cal.ErrPast):
		return "past"
	case errors.Is(err, ErrNotDue):
		return "notdue"
	case err == nil:
		return "ok"
	default:
		return err.Error()
	}
}

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(1234, 5678))
	for iter := 0; iter < 60; iter++ {
		open := rng.Int64N(600)
		closeV := open + 60 + rng.Int64N(900)
		if closeV > 1440 {
			closeV = 1440
		}
		mask := uint8(1 + rng.IntN(127))
		c := cal.New(open, closeV, mask)
		mgr, sm := NewManager(c)
		nv := newNaive(open, closeV, mask)

		const horizon = int64(40 * 1440)
		var now int64
		ids := []string{"a", "b", "c"}

		advance := func(step int64) {
			now += step
			if now > horizon {
				now = horizon
			}
		}

		for op := 0; op < 400; op++ {
			switch rng.IntN(10) {
			case 0:
				id := ids[rng.IntN(len(ids))]
				budget := 1 + rng.Int64N(4000)
				err := sm.Start([]byte(id), budget, now)
				nerr := "ok"
				if tm := nv.timers[id]; tm == nil {
					nv.timers[id] = &ntimer{budget: budget,
						segs: []nseg{{start: now}}, ack: map[int]bool{}}
				} else {
					nerr = "exists"
				}
				if catErr(err) != nerr {
					t.Fatalf("iter%d op%d Start(%s,B=%d,t=%d): got %s want %s",
						iter, op, id, budget, now, catErr(err), nerr)
				}
				t.Logf("iter%d Start(%s,B=%d,t=%d) => %s", iter, id, budget, now, catErr(err))
			case 1:
				id := ids[rng.IntN(len(ids))]
				err := sm.Pause([]byte(id), now)
				tm := nv.timers[id]
				nerr := "ok"
				if tm == nil {
					nerr = "notfound"
				} else if tm.paused {
					nerr = "state"
				} else {
					tm.segs[len(tm.segs)-1].end = now
					tm.paused = true
				}
				if catErr(err) != nerr {
					t.Fatalf("Pause mismatch %s!=%s", catErr(err), nerr)
				}
			case 2:
				id := ids[rng.IntN(len(ids))]
				err := sm.Resume([]byte(id), now)
				tm := nv.timers[id]
				nerr := "ok"
				if tm == nil {
					nerr = "notfound"
				} else if !tm.paused {
					nerr = "state"
				} else {
					tm.segs = append(tm.segs, nseg{start: now})
					tm.paused = false
				}
				if catErr(err) != nerr {
					t.Fatalf("Resume mismatch %s!=%s", catErr(err), nerr)
				}
			case 3:
				day := now/1440 + int64(rng.IntN(5)-1)
				err := c.AddHoliday(day, now)
				nerr := "ok"
				if day < 0 || day > cal.MaxTime/1440 {
					nerr = "invalid"
				} else if day <= now/1440 {
					nerr = "past"
				} else if !nv.hol[day] && len(nv.hol) >= cal.MaxHolidays {
					nerr = "invalid"
				} else {
					nv.hol[day] = true
				}
				if catErr(err) != nerr {
					t.Fatalf("iter%d AddHoliday(day=%d,t=%d): got %s want %s",
						iter, day, now, catErr(err), nerr)
				}
				t.Logf("iter%d AddHoliday(%d,t=%d) => %s", iter, day, now, catErr(err))
			case 4:
				id := ids[rng.IntN(len(ids))]
				q := []int{50, 80, 100, 30, 50}[rng.IntN(5)]
				err := mgr.Ack([]byte(id), q, now)
				tm := nv.timers[id]
				nerr := "ok"
				if q != 50 && q != 80 && q != 100 {
					nerr = "invalid"
				} else if tm == nil {
					nerr = "notfound"
				} else if t, ok := nv.trigger(tm, threshold(tm.budget, q), now); !ok || t > now {
					nerr = "notdue"
				} else {
					tm.ack[q] = true
				}
				if catErr(err) != nerr {
					t.Fatalf("iter%d Ack(%s,%d,t=%d): got %s want %s",
						iter, id, q, now, catErr(err), nerr)
				}
			case 5:
				id := ids[rng.IntN(len(ids))]
				got, gerr := sm.Elapsed([]byte(id), now)
				tm := nv.timers[id]
				if tm == nil {
					if !errors.Is(gerr, sla.ErrNotFound) {
						t.Fatalf("Elapsed missing: %v", gerr)
					}
				} else if w := nv.elapsed(tm, now); got != w {
					t.Fatalf("iter%d Elapsed(%s,t=%d)=%d want %d", iter, id, now, got, w)
				}
			case 6:
				id := ids[rng.IntN(len(ids))]
				got, ok, gerr := sm.Deadline([]byte(id), now)
				tm := nv.timers[id]
				if tm == nil {
					if !errors.Is(gerr, sla.ErrNotFound) {
						t.Fatalf("Deadline missing: %v", gerr)
					}
				} else if tm.paused {
					if ok {
						t.Fatalf("paused deadline must be absent, got %d", got)
					}
				} else if dt, wok := nv.trigger(tm, tm.budget, now); wok {
					if !ok || got != dt {
						t.Fatalf("iter%d Deadline(%s,t=%d)=%d(%v) want %d",
							iter, id, now, got, ok, dt)
					}
				}
			default:
				got, gerr := mgr.Tick(now)
				if gerr != nil {
					t.Fatalf("Tick: %v", gerr)
				}
				want := nv.tick(now)
				if !eqAlarms(got, want) {
					t.Fatalf("iter%d op%d Tick(t=%d) mismatch:\n got %v\nwant %v\nparams o=%d c=%d m=%b hol=%v",
						iter, op, now, got, want, open, closeV, mask, nv.hol)
				}
				t.Logf("iter%d Tick(t=%d) => %d alarms (matches naive)", iter, now, len(got))
			}
			advance(1 + rng.Int64N(300))
			nv.clock = now
		}
	}
	fmt.Println("60 random operation sequences match naive minute simulation")
}
