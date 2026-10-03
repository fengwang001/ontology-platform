package tier

import (
	"math/rand"
	"testing"
)

func TestNaiveDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 50; iter++ {
		a0 := int64(60000 + rng.Intn(10)*60000)
		a1 := a0 + int64(rng.Intn(5))*3600000
		a2 := a1 + int64(rng.Intn(5))*3600000
		s, n := New(a0, a1, a2, 40), newNaive(a0, a1, a2, 40)
		type op struct {
			k           byte
			ts, v, f, t int64
			id          string
		}
		var log []op
		for step := 0; step < 200; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				ts, v := rng.Int63n(4*3600000), rng.Int63n(2001)-1000
				es, en := errClass(s.Write(ts, v)), errClass(n.write(ts, v))
				if es != en {
					t.Fatalf("it%d st%d write(%d,%d) mismatch\n%s", iter, step, ts, v, dump(s))
				} else if es == "ok" {
					log = append(log, op{k: 'w', ts: ts, v: v})
				}
			case 5:
				now := s.Clock() + int64(1+rng.Intn(20))*60000
				if err := s.Advance(now); err != nil {
					t.Fatal(err)
				}
				n.advance(now)
				log = append(log, op{k: 'a', ts: now})
			case 6:
				id := "h" + itoa2(int64(rng.Intn(4)))
				f := rng.Int63n(3 * 3600000)
				tm := f + int64(1+rng.Intn(4))*60000
				dup := false
				for _, h := range n.holds {
					dup = dup || h.id == id
				}
				err := s.Holds().Hold(id, f, tm, "u")
				if (err == nil) == dup {
					t.Fatalf("hold %s dup=%v err=%v", id, dup, err)
				}
				if err == nil {
					n.holds = append(n.holds, nHold{id, f, tm})
					log = append(log, op{k: 'h', f: f, t: tm, id: id})
				}
			case 7:
				id := "h" + itoa2(int64(rng.Intn(4)))
				if err := s.Holds().Release(id, "u"); err == nil {
					for i, h := range n.holds {
						if h.id == id {
							n.holds = append(n.holds[:i], n.holds[i+1:]...)
							log = append(log, op{k: 'r', id: id})
						}
					}
				}
			default:
				f := rng.Int63n(3 * 3600000)
				tm := f + int64(1+rng.Intn(4))*60000
				got, err := s.Query(f, tm)
				if err != nil {
					t.Fatal(err)
				}
				if w := n.query(f, tm); got != w {
					t.Fatalf("it%d q[%d,%d) %+v vs %+v\n%s", iter, f, tm, got, w, dump(s))
				}
				note(t, "it%d q[%d,%d)=%+v", iter, f, tm, got)
			}
			if s.Units() > 40 || dump(s) != dumpNaive(n) {
				t.Fatalf("it%d st%d cap=%d\nreal %s\nnaive%s", iter, step, s.Units(), dump(s), dumpNaive(n))
			}
		}
		// 收敛性：合并连续 Advance 重放，最终状态与逐次推进一致。
		target := s.Clock() + 10*3600000
		y := New(a0, a1, a2, 40)
		pending := int64(-1)
		replay := func() {
			if pending >= 0 {
				_ = y.Advance(pending)
			}
		}
		for _, o := range log {
			if o.k == 'a' {
				pending = o.ts
				continue
			}
			replay()
			pending = -1
			switch o.k {
			case 'w':
				if err := y.Write(o.ts, o.v); err != nil {
					t.Fatalf("replay write(%d,%d) at clock %d: %v\n%s", o.ts, o.v, y.Clock(), err, dump(y))
				}
			case 'h':
				_ = y.Holds().Hold(o.id, o.f, o.t, "u")
			case 'r':
				_ = y.Holds().Release(o.id, "u")
			}
		}
		_ = y.Advance(target)
		_ = s.Advance(target)
		if dump(s) != dump(y) {
			t.Fatalf("convergence it%d\n%s\n%s", iter, dump(s), dump(y))
		}
	}
}

func errClass(e error) string {
	switch e {
	case nil:
		return "ok"
	case ErrExpired:
		return "expired"
	case ErrCapacity:
		return "capacity"
	case ErrOverflow:
		return "overflow"
	default:
		return "other"
	}
}

func itoa2(v int64) string {
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		out = string(rune('0'+v%10)) + out
		v /= 10
	}
	return out
}

// 保全冻结折叠与删除；半开两端相切不冻结；解除后下一次 Advance 照常折叠。
func TestHoldFreezeAndRelease(t *testing.T) {
	s := New(60000, 120000, 3600000, 1000)
	must(t, s.Write(100, 5))
	must(t, s.Holds().Hold("h", 40000, 45000, "u"))
	must(t, s.Advance(120000))
	if len(s.l0) != 1 {
		t.Fatalf("held minute must stay L0: %s", dump(s))
	}
	if err := s.Write(200, 7); err != nil || len(s.l0) != 2 {
		t.Fatalf("late write under hold -> L0, err=%v %s", err, dump(s))
	}
	must(t, s.Holds().Release("h", "u"))
	must(t, s.Advance(120001))
	if len(s.l0) != 0 || s.l1[0].Count != 2 {
		t.Fatalf("after release minute should fold: %s", dump(s))
	}
	s2 := New(60000, 120000, 3600000, 1000)
	must(t, s2.Write(100, 1))
	must(t, s2.Holds().Hold("g", 60000, 120000, "u"))
	must(t, s2.Advance(120000))
	if len(s2.l0) != 0 {
		t.Fatalf("touch-only must not freeze minute 0: %s", dump(s2))
	}
}

// Advance 注入 panic：recover 返回中止错误，状态与时钟逐位不变。
func TestAdvancePanicRollback(t *testing.T) {
	s := New(60000, 120000, 3600000, 1000)
	_ = s.Write(100, 1)
	_ = s.Write(61000, 2)
	before := s.snapshot()
	s.SetHook(func(step string) {
		if step == "fold0" {
			panic("injected")
		}
	})
	if err := s.Advance(120000); err != ErrAborted {
		t.Fatalf("Advance = %v want ErrAborted", err)
	}
	if s.Clock() != 0 || dump(s) != dumpState(before) {
		t.Fatalf("state changed after panic: clock=%d %s", s.Clock(), dump(s))
	}
	s.SetHook(nil)
	must(t, s.Advance(120000))
	if s.l1[0].Count != 1 || len(s.l0) != 1 {
		t.Fatalf("post-rollback advance wrong: %s", dump(s))
	}
}

func dumpState(st state) string {
	return dump(&Store{l0: st.l0, l1: st.l1, l2: st.l2})
}

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
