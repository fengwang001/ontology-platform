package fdb

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type op struct {
	kind      string
	p, v      int
	s, d, mac MAC
	t         int64
}

type model interface {
	Frame(p int, s, d MAC, v int, t int64) ([]int, error)
	AddStatic(v int, mac MAC, p int, t int64) error
	FlushPort(p int, t int64) (int, error)
	Lookup(v int, mac MAC, t int64) (int, bool, error)
	Len() int
	Stats() Stats
}

func runOp(m model, o op) (out []int, ret int, ok bool, err error) {
	switch o.kind {
	case "frame":
		out, err = m.Frame(o.p, o.s, o.d, o.v, o.t)
	case "static":
		err = m.AddStatic(o.v, o.mac, o.p, o.t)
	case "flush":
		ret, err = m.FlushPort(o.p, o.t)
	case "lookup":
		ret, ok, err = m.Lookup(o.v, o.mac, o.t)
	}
	return
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b)
}

func checkConservation(t *testing.T, where string, l int, s Stats) {
	t.Helper()
	lhs := int64(l) + s.Evictions + s.Expired + s.Flushed + s.Overridden
	if lhs != s.Learned {
		t.Fatalf("守恒式破坏 @%s: Len+Evictions+Expired+Flushed+Overridden=%d != Learned=%d (%+v)",
			where, lhs, s.Learned, s)
	}
}

func opString(o op) string {
	switch o.kind {
	case "frame":
		return fmt.Sprintf("Frame(p=%d,s=%x,d=%x,v=%d,t=%d)", o.p, o.s, o.d, o.v, o.t)
	case "static":
		return fmt.Sprintf("AddStatic(v=%d,mac=%x,p=%d,t=%d)", o.v, o.mac, o.p, o.t)
	case "flush":
		return fmt.Sprintf("FlushPort(p=%d,t=%d)", o.p, o.t)
	default:
		return fmt.Sprintf("Lookup(v=%d,mac=%x,t=%d)", o.v, o.mac, o.t)
	}
}

// TestDifferentialRandom 随机操作序列在 FDB 与朴素实现上重放，
// 逐步比对返回值、Len、计数，并检查守恒式；日志打印输入/输出/判定依据。
func TestDifferentialRandom(t *testing.T) {
	const n, capC = 5, 4
	const ageA int64 = 30

	macs := make([]MAC, 12)
	for i := range macs {
		macs[i] = MAC{byte(i % 3), 0, 0, 0, 0, byte(i + 1)}
	}

	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		real, _ := New(n, ageA, capC)
		naive, _ := NewNaive(n, ageA, capC)
		clock := int64(0)

		for step := 0; step < 400; step++ {
			o := op{}
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				o.kind = "frame"
				o.p = rng.Intn(n + 1)
				o.v = 1 + rng.Intn(4094)
				if rng.Intn(20) == 0 {
					o.v = rng.Intn(2) * 4095
				}
				o.s = macs[rng.Intn(len(macs))]
				o.d = macs[rng.Intn(len(macs))]
			case 5, 6:
				o.kind = "static"
				o.p = rng.Intn(n)
				o.v = 1 + rng.Intn(4094)
				o.mac = macs[rng.Intn(len(macs))]
			case 7:
				o.kind = "flush"
				o.p = rng.Intn(n)
			default:
				o.kind = "lookup"
				o.v = 1 + rng.Intn(4094)
				o.mac = macs[rng.Intn(len(macs))]
			}
			if o.kind != "lookup" {
				if rng.Intn(10) == 0 {
					clock -= int64(rng.Intn(5) + 1)
				} else {
					clock += int64(rng.Intn(40))
				}
				o.t = clock
			} else {
				o.t = clock + int64(rng.Intn(60)) - 20
			}

			rOut, rRet, rOk, rErr := runOp(real, o)
			gOut, gRet, gOk, gErr := runOp(naive, o)
			where := fmt.Sprintf("seed=%d step=%d %s", seed, step, opString(o))
			if !reflect.DeepEqual(rOut, gOut) || rRet != gRet || rOk != gOk || !sameErr(rErr, gErr) {
				t.Fatalf("差分不一致 @%s\n real=(%v,%d,%v,%v)\nnaive=(%v,%d,%v,%v)",
					where, rOut, rRet, rOk, rErr, gOut, gRet, gOk, gErr)
			}
			rl, gl := real.Len(), naive.Len()
			rs, gs := real.Stats(), naive.Stats()
			if rl != gl || rs != gs {
				t.Fatalf("状态不一致 @%s Len(%d vs %d) Stats(%+v vs %+v)", where, rl, gl, rs, gs)
			}
			checkConservation(t, where, rl, rs)
			if testing.Verbose() && step < 20 {
				t.Logf("输入 %s -> 输出 out=%v ret=%d ok=%v err=%v | Len=%d Stats=%+v",
					opString(o), rOut, rRet, rOk, rErr, rl, rs)
			}
		}
	}
}

// TestReplayDeterministic 相同操作序列重放两次，返回值与计数完全一致。
func TestReplayDeterministic(t *testing.T) {
	const n, capC = 6, 3
	const ageA int64 = 25
	ops := buildOps(n, capC, 7)

	run := func() ([]string, []Stats, []int) {
		f, _ := New(n, ageA, capC)
		var logs []string
		var stats []Stats
		var lens []int
		for _, o := range ops {
			out, ret, ok, err := runOp(f, o)
			logs = append(logs, fmt.Sprintf("%s -> out=%v ret=%d ok=%v err=%v", opString(o), out, ret, ok, err))
			stats = append(stats, f.Stats())
			lens = append(lens, f.Len())
		}
		return logs, stats, lens
	}
	l1, s1, n1 := run()
	l2, s2, n2 := run()
	if !reflect.DeepEqual(l1, l2) || !reflect.DeepEqual(s1, s2) || !reflect.DeepEqual(n1, n2) {
		t.Fatalf("重放不确定:\nrun1=%v\nrun2=%v", l1, l2)
	}
	t.Logf("重放 %d 个操作，两次返回值/Len/计数完全一致；首条: %s", len(ops), l1[0])
}

func buildOps(n, c int, seed int64) []op {
	rng := rand.New(rand.NewSource(seed))
	macs := make([]MAC, 10)
	for i := range macs {
		macs[i] = MAC{byte(i % 2), 0, 0, 0, 0, byte(i + 1)}
	}
	var ops []op
	clock := int64(0)
	for step := 0; step < 300; step++ {
		o := op{}
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			o.kind = "frame"
			o.p = rng.Intn(n)
			o.v = 1 + rng.Intn(16)
			o.s = macs[rng.Intn(len(macs))]
			o.d = macs[rng.Intn(len(macs))]
		case 5, 6:
			o.kind = "static"
			o.p = rng.Intn(n)
			o.v = 1 + rng.Intn(16)
			o.mac = macs[rng.Intn(len(macs))]
		case 7:
			o.kind = "flush"
			o.p = rng.Intn(n)
		default:
			o.kind = "lookup"
			o.v = 1 + rng.Intn(16)
			o.mac = macs[rng.Intn(len(macs))]
		}
		clock += int64(rng.Intn(40))
		o.t = clock
		ops = append(ops, o)
	}
	return ops
}

// TestConcurrentSafe 并发调用不触发竞态，且最终状态等价于某个串行顺序：
// 所有变更调用使用非递减的全局时钟，最终守恒式成立、计数自洽。
func TestConcurrentSafe(t *testing.T) {
	f := mustNew(t, 8, 1_000, 16)
	const goroutines, rounds = 8, 200
	var wg sync.WaitGroup
	var clkMu sync.Mutex
	clock := int64(0)
	nextT := func() int64 {
		clkMu.Lock()
		clock++
		v := clock
		clkMu.Unlock()
		return v
	}
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id) + 99))
			for i := 0; i < rounds; i++ {
				p := rng.Intn(8)
				v := 1 + rng.Intn(8)
				m1 := MAC{byte(id), 0, 0, 0, byte(i >> 8), byte(i)}
				m2 := MAC{0, byte(id), 0, 0, byte(i >> 8), byte(i)}
				switch rng.Intn(4) {
				case 0:
					_, _ = f.Frame(p, m1, m2, v, nextT())
				case 1:
					_ = f.AddStatic(v, m1, p, nextT())
				case 2:
					_, _ = f.FlushPort(p, nextT())
				default:
					_, _, _ = f.Lookup(v, m2, nextT())
				}
			}
		}(g)
	}
	wg.Wait()
	s := f.Stats()
	checkConservation(t, "concurrent-end", f.Len(), s)
	t.Logf("并发结束: %+v Len=%d（恒等串行顺序由互斥保证）", s, f.Len())
}
