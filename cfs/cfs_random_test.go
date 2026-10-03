package cfs

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// compareAll 比对控制器与朴素模拟的全部可观察状态。
func compareAll(t *testing.T, seq, op int, c *Controller, n *naive) {
	t.Helper()
	fail := false
	if g := c.Pool(); g != n.g {
		t.Errorf("seq=%d op=%d Pool()=%d, 朴素模拟=%d", seq, op, g, n.g)
		fail = true
	}
	p, nth, tt := c.Stats()
	if p != n.periods || nth != n.nThrottled || tt != n.throttledTime {
		t.Errorf("seq=%d op=%d Stats()=(%d,%d,%d), 朴素模拟=(%d,%d,%d)",
			seq, op, p, nth, tt, n.periods, n.nThrottled, n.throttledTime)
		fail = true
	}
	for i := range n.cpus {
		st, l, since := c.State(i)
		nc := n.cpus[i]
		if st != nc.state || l != nc.l || since != nc.since {
			t.Errorf("seq=%d op=%d State(%d)=(%v,%d,%d), 朴素模拟=(%v,%d,%d)",
				seq, op, i, st, l, since, nc.state, nc.l, nc.since)
			fail = true
		}
	}
	gotQ := c.Queue()
	if len(gotQ) != 0 || len(n.queue) != 0 {
		if !reflect.DeepEqual(gotQ, n.queue) {
			t.Errorf("seq=%d op=%d Queue()=%v, 朴素模拟=%v", seq, op, gotQ, n.queue)
			fail = true
		}
	}
	if fail {
		t.Fatalf("seq=%d op=%d 状态不一致", seq, op)
	}
}

// checkInvariants 校验题目要求的不变量。
func checkInvariants(t *testing.T, seq, op int, c *Controller, n *naive) {
	t.Helper()
	g := c.Pool()
	if g < 0 || g > n.q+n.b {
		t.Fatalf("seq=%d op=%d G=%d 越出 [0,%d]", seq, op, g, n.q+n.b)
	}
	inQueue := make(map[int]bool)
	for _, id := range c.Queue() {
		inQueue[id] = true
	}
	for i := range n.cpus {
		st, l, _ := c.State(i)
		if l > n.s {
			t.Fatalf("seq=%d op=%d cpu%d l=%d 超过 S=%d", seq, op, i, l, n.s)
		}
		if st == Throttled {
			if l > 0 {
				t.Fatalf("seq=%d op=%d 节流 cpu%d l=%d>0", seq, op, i, l)
			}
			if !inQueue[i] {
				t.Fatalf("seq=%d op=%d 节流 cpu%d 不在队列", seq, op, i)
			}
		} else {
			if inQueue[i] {
				t.Fatalf("seq=%d op=%d 非节流 cpu%d 在队列中", seq, op, i)
			}
			everRan := n.consumed[i] > 0 || n.borrowed[i] > 0
			if everRan && l <= 0 {
				t.Fatalf("seq=%d op=%d 非节流且运行过的 cpu%d l=%d<=0", seq, op, i, l)
			}
		}
		// l 恒等于 累计借得 - 累计归还 - 累计消耗
		if want := n.borrowed[i] - n.returned[i] - n.consumed[i]; l != want {
			t.Fatalf("seq=%d op=%d cpu%d l=%d, 核算值=%d", seq, op, i, l, want)
		}
	}
	// throttledTime 等于所有已解除节流事件的 b-since 之和
	var sum int64
	for _, v := range n.unthrottles {
		sum += v
	}
	if _, _, tt := c.Stats(); tt != sum {
		t.Fatalf("seq=%d op=%d throttledTime=%d, 事件求和=%d", seq, op, tt, sum)
	}
}

// TestRandomAgainstNaive 2000 组随机操作序列与朴素模拟对照，
// 日志打印每个操作的输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for seq := 0; seq < 2000; seq++ {
		q := 1 + rng.Int63n(20)
		p := 1 + rng.Int63n(20)
		s := 1 + rng.Int63n(10)
		b := rng.Int63n(20)
		ncpu := 1 + rng.Intn(4)
		c, err := New(q, p, s, b, ncpu)
		if err != nil {
			t.Fatalf("seq=%d New 被拒: %v", seq, err)
		}
		n := newNaive(q, p, s, b, ncpu)
		t.Logf("seq=%d 配置 Q=%d P=%d S=%d B=%d C=%d", seq, q, p, s, b, ncpu)

		now := int64(0)
		ops := 10 + rng.Intn(20)
		for op := 0; op < ops; op++ {
			now += rng.Int63n(20)
			cpuID := rng.Intn(ncpu)
			d := 1 + rng.Int63n(30)
			opNow := now
			// 小概率注入非法输入，覆盖各类拒绝路径
			switch r := rng.Intn(100); {
			case r < 3:
				cpuID = -1 + rng.Intn(2)*(ncpu+1) // -1 或 ncpu
			case r < 6:
				if rng.Intn(2) == 0 {
					opNow = -1
				} else {
					opNow = maxNow + 1
				}
			case r < 9:
				if rng.Intn(2) == 0 {
					d = 0
				} else {
					d = maxRun + 1
				}
			case r < 12 && now > 0:
				opNow = now - 1 - rng.Int63n(5) // 时间回退
			}

			var gotErr, wantErr error
			var input, trace string
			switch rng.Intn(3) {
			case 0:
				input = fmt.Sprintf("Wake(now=%d,cpu=%d)", opNow, cpuID)
				gotErr = c.Wake(opNow, cpuID)
				wantErr, trace = n.wake(opNow, cpuID)
			case 1:
				input = fmt.Sprintf("Run(now=%d,cpu=%d,d=%d)", opNow, cpuID, d)
				gotErr = c.Run(opNow, cpuID, d)
				wantErr, trace = n.run(opNow, cpuID, d)
			default:
				input = fmt.Sprintf("Idle(now=%d,cpu=%d)", opNow, cpuID)
				gotErr = c.Idle(opNow, cpuID)
				wantErr, trace = n.idle(opNow, cpuID)
			}
			t.Logf("seq=%d op=%d 输入=%s 输出=%v 判定依据: %s", seq, op, input, gotErr, trace)
			if !sameErr(gotErr, wantErr) {
				t.Fatalf("seq=%d op=%d %s: 控制器=%v, 朴素模拟=%v",
					seq, op, input, gotErr, wantErr)
			}
			compareAll(t, seq, op, c, n)
			checkInvariants(t, seq, op, c, n)
		}
	}
}
