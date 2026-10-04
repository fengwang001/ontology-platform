package sched_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/sched"
)

type op struct {
	kind                  string
	dev, id               string
	P, O, W, size, expire int64
	prio, now             int64
}

func (o op) String() string {
	switch o.kind {
	case "register":
		return fmt.Sprintf("Register(%s,P=%d,o=%d,w=%d,now=%d)", o.dev, o.P, o.O, o.W, o.now)
	case "reconfig":
		return fmt.Sprintf("Reconfigure(%s,P=%d,o=%d,w=%d,now=%d)", o.dev, o.P, o.O, o.W, o.now)
	case "enqueue":
		return fmt.Sprintf("Enqueue(%s,%s,size=%d,prio=%d,expire=%d,now=%d)",
			o.dev, o.id, o.size, o.prio, o.expire, o.now)
	case "deliver":
		return fmt.Sprintf("Deliver(%s,now=%d)", o.dev, o.now)
	case "ack":
		return fmt.Sprintf("Ack(%s,%s,now=%d)", o.dev, o.id, o.now)
	}
	return o.kind
}

func genOps(rng *rand.Rand) []op {
	const horizon = 600
	n := 20 + rng.Intn(25)
	ops := []op{{kind: "register", dev: "d",
		P: int64(5 + rng.Intn(36)), O: 0, W: 0, now: 0}}
	// 初始偏移/窗长随机。
	ops[0].P = int64(5 + rng.Intn(36))
	ops[0].O = int64(rng.Int63n(ops[0].P))
	ops[0].W = int64(1 + rng.Int63n(ops[0].P))

	var now int64
	ids := []string{}
	liveIds := []string{}
	nextID := 0
	advance := func() int64 {
		if rng.Intn(8) != 0 {
			now += int64(rng.Intn(12))
			if now > horizon {
				now = horizon
			}
		}
		return now
	}
	for i := 0; i < n; i++ {
		r := rng.Intn(100)
		switch {
		case r < 45:
			advance()
			id := fmt.Sprintf("cmd%d", nextID)
			nextID++
			size := int64(1 + rng.Intn(80))
			prio := rng.Intn(4)
			expire := now + int64(rng.Intn(220)-40)
			if expire < 0 {
				expire = 0
			}
			use := id
			liveCopy := append([]string{}, liveIds...)
			if len(liveCopy) > 0 && rng.Intn(5) == 0 {
				use = liveCopy[rng.Intn(len(liveCopy))] // 故意重复 id
			}
			if rng.Intn(15) == 0 {
				use = "" // 故意非法 id
			}
			ops = append(ops, op{kind: "enqueue", dev: "d", id: use,
				size: size, prio: int64(prio), expire: expire, now: now})
			if use != "" {
				ids = append(ids, use)
			}
		case r < 75:
			advance()
			ops = append(ops, op{kind: "deliver", dev: "d", now: now})
		case r < 90:
			advance()
			id := "missing"
			if len(ids) > 0 {
				id = ids[rng.Intn(len(ids))]
			}
			liveIds = append(liveIds[:0:0], liveIds...)
			ops = append(ops, op{kind: "ack", dev: "d", id: id, now: now})
		default:
			advance()
			P := int64(5 + rng.Intn(36))
			O := int64(rng.Int63n(P))
			W := int64(1 + rng.Int63n(P))
			ops = append(ops, op{kind: "reconfig", dev: "d", P: P, O: O, W: W, now: now})
		}
		_ = ids
	}
	return ops
}

func runNaive(ops []op, K, R int, Bw int64, Q int) []string {
	n := &naive{K: K, R: R, Bw: Bw, Q: Q, devs: map[string]*nDev{}}
	trace := []string{}
	for _, o := range ops {
		switch o.kind {
		case "register":
			e := n.register(o.dev, o.P, o.O, o.W, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, e))
		case "reconfig":
			e := n.reconfig(o.dev, o.P, o.O, o.W, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, e))
		case "enqueue":
			e := n.enqueue(o.dev, o.id, o.size, int(o.prio), o.expire, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, e))
		case "ack":
			e := n.ack(o.dev, o.id, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, e))
		case "deliver":
			r := n.deliver(o.dev, o.now)
			if r.err != "" {
				trace = append(trace, fmt.Sprintf("%s => err=%s", o, r.err))
			} else {
				trace = append(trace, fmt.Sprintf("%s => err=ok del=%v exp=%v fail=%v examined=%d",
					o, r.delivered, r.expired, r.failed, r.examined))
			}
		}
	}
	return trace
}

func runProd(t *testing.T, sched0 *sched.Scheduler, ops []op) []string {
	t.Helper()
	trace := []string{}
	for _, o := range ops {
		switch o.kind {
		case "register":
			e := sched0.Register(o.dev, o.P, o.O, o.W, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, errName(e)))
		case "reconfig":
			e := sched0.Reconfigure(o.dev, o.P, o.O, o.W, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, errName(e)))
		case "enqueue":
			e := sched0.Enqueue(o.dev, o.id, o.size, int(o.prio), o.expire, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, errName(e)))
		case "ack":
			e := sched0.Ack(o.dev, o.id, o.now)
			trace = append(trace, fmt.Sprintf("%s => %s", o, errName(e)))
		case "deliver":
			v, e := sched0.Deliver(o.dev, o.now)
			if e != nil {
				trace = append(trace, fmt.Sprintf("%s => err=%s", o, errName(e)))
			} else {
				st, _ := sched0.LastSettled(o.dev)
				if v == nil {
					v = []string{}
				}
				trace = append(trace, fmt.Sprintf("%s => err=ok del=%v exp=%v fail=%v examined=%d",
					o, v, st.Expired, st.Failed, sched0.Examined()))
			}
		}
	}
	return trace
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 1500; iter++ {
		ops := genOps(rng)
		K := 1 + rng.Intn(4)
		Bw := int64(20 + rng.Intn(120))
		R := 1 + rng.Intn(3)
		Q := 1 + rng.Intn(20)
		s := sched.New(K, Bw, R, Q)
		gotTrace := runProd(t, s, ops)
		wantTrace := runNaive(ops, K, R, Bw, Q)
		if !reflect.DeepEqual(gotTrace, wantTrace) {
			for i := range wantTrace {
				mark := "  "
				if i >= len(gotTrace) || gotTrace[i] != wantTrace[i] {
					mark = ">>"
				}
				t.Logf("%s 输入/输出 朴素: %s", mark, wantTrace[i])
				if i < len(gotTrace) {
					t.Logf("%s 输入/输出 生产: %s", mark, gotTrace[i])
				}
			}
			t.Fatalf("序列 %d 与朴素模拟不一致（判定依据：逐条输入/输出/错误身份/清单/examined 全等）; K=%d Bw=%d R=%d Q=%d",
				iter, K, Bw, R, Q)
		}
		if iter < 3 || iter%200 == 0 {
			t.Logf("序列 %d 通过（%d 个操作，判定：全部输入/输出与朴素模型一致）", iter, len(ops))
		}
	}
}

// TestExaminedBound：Deliver 考察指令数 ≤ 返回条数+本次过期数+本次失败数+1，
// 且与队列长度无关——队列 100 与 10000 两档 examined 完全相同。
func TestExaminedBound(t *testing.T) {
	measure := func(t *testing.T, n int) (examined, delivered, expired, failed, totalChecks int) {
		// K=2 Bw=1000 R=3 Q=n+10；窗口 P=50,o=0,w=50（窗口覆盖全部时间，
		// 便于连续 Deliver）。
		s := sched.New(2, 1000, 3, n+10)
		if err := s.Register("d", 50, 0, 50, 0); err != nil {
			t.Fatal(err)
		}
		// 前面 n 条都用 600 字节：取走第一条后第二条 600 放不下即停
		// （队首阻塞）；尾部 1 字节小指令同 prio，按 seq 排最后，无法
		// 越过阻塞的队首。
		for i := 0; i < n; i++ {
			if err := s.Enqueue("d", fmt.Sprintf("b%d", i), 600, 0, 100000, 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Enqueue("d", "small1", 1, 0, 100000, 0); err != nil {
			t.Fatal(err)
		}
		// 第一次 Deliver：第一条考察后取出（600），第二条 600 放不下停。
		v, err := s.Deliver("d", 0)
		if err != nil || len(v) != 1 {
			t.Fatalf("first deliver %v %v", v, err)
		}
		examined = s.Examined()
		delivered = len(v)
		bound := delivered + 1
		if examined > bound {
			t.Fatalf("n=%d examined=%d > 投递%d+过期0+失败0+1=%d", n, examined, delivered, bound)
		}
		totalChecks += examined
		// 同窗再 Deliver：无重投、无未投递可取（队首仍是 600），考察 1。
		_, _ = s.Deliver("d", 1)
		examined2 := s.Examined()
		if examined2 != 1 {
			t.Fatalf("n=%d same-window examined=%d want 1（与队列长度无关）", n, examined2)
		}
		totalChecks += examined2
		t.Logf("队列规模 n=%d：首次 Deliver examined=%d（del=1,exp=0,fail=0,上界2），同窗再取 examined=%d",
			n, examined, examined2)
		return examined, delivered, expired, failed, totalChecks
	}

	e100, d100, _, _, _ := measure(t, 100)
	e10000, d10000, _, _, _ := measure(t, 10000)
	if e100 != e10000 || d100 != d10000 {
		t.Fatalf("examined 应与队列长度无关：n=100 -> %d，n=10000 -> %d", e100, e10000)
	}
	t.Logf("判定依据：两档 examined 均为 %d，与队列长度无关，且 ≤ 投递+过期+失败+1", e100)
}
