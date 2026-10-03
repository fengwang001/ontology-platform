package picker

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// op 是差分测试的一步操作。
type op struct {
	kind                  int // 0 add 1 remove 2 pick 3 release
	id                    string
	r1, r2                uint64
	ticket                int64
	rtt                   int64
	ok                    bool
	now                   int64
	badID, badRTT, badNow bool
}

// TestDifferentialAgainstNaive 用 2000 组随机操作序列，逐步对照真实选择器
// 与独立朴素模型；每个序列固定种子，可精确复现。出现分歧时打印全部
// 已执行操作的输入、两侧输出与判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		seed := int64(20261003 + seq)
		rng := rand.New(rand.NewSource(seed))
		runOneDifferential(t, int64(seq), seed, rng)
	}
}

func runOneDifferential(t *testing.T, seq, seed int64, rng *rand.Rand) {
	t.Helper()

	tau := int64(1 + rng.Intn(300))
	p0 := int64(1 + rng.Intn(500))
	pf := int64(1 + rng.Intn(5000))
	m := int64(1 + rng.Intn(4))
	nmax := 1 + rng.Intn(6)

	cfg := Config{Tau: tau, Prior: p0, FailPenalty: pf, MaxInflight: m, MaxEndpoints: nmax}
	real, err := New(cfg)
	if err != nil {
		t.Fatalf("seq=%d seed=%d New: %v", seq, seed, err)
	}
	naive := newNaive(tau, p0, pf, m, nmax)

	// 候选 id 池（字节序与字典序需注意：用零填充保证顺序确定）。
	idPool := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	rng.Shuffle(len(idPool), func(i, j int) { idPool[i], idPool[j] = idPool[j], idPool[i] })

	steps := 40 + rng.Intn(80)
	now := int64(0)
	var log []op
	var openTickets []int64 // 成功 pick 颁发、尚未归还的票据（两侧一致）

	failf := func(at int, msg string, args ...any) {
		t.Errorf("DIFF seq=%d seed=%d cfg=%+v step=%d: %s\nOPS:\n%s",
			seq, seed, cfg, at, fmt.Sprintf(msg, args...), formatOps(log))
	}

	for s := 0; s < steps; s++ {
		var o op
		k := rng.Intn(100)
		switch {
		case k < 25:
			o.kind = 0
			o.id = idPool[rng.Intn(len(idPool))]
			if rng.Intn(20) == 0 {
				o.badID = true
			}
		case k < 40:
			o.kind = 1
			o.id = idPool[rng.Intn(len(idPool))]
			if rng.Intn(20) == 0 {
				o.badID = true
			}
		case k < 75:
			o.kind = 2
			now += int64(rng.Intn(int(tau*2 + 10)))
			if now > maxNow {
				now = maxNow
			}
			o.now = now
			o.r1 = rng.Uint64()
			o.r2 = rng.Uint64()
		default:
			o.kind = 3
			if len(openTickets) > 0 {
				switch rng.Intn(6) {
				case 0:
					// 故意使用未知票据，应被两侧拒绝且不改状态。
					o.ticket = int64(1 << 40)
				default:
					idx := rng.Intn(len(openTickets))
					o.ticket = openTickets[idx]
				}
			} else {
				// 无在途票据：用一个真实 Pick 永不产生的小号之外的值。
				o.ticket = int64(1<<40) + int64(s)
			}
			now += int64(rng.Intn(int(tau*2 + 10)))
			if now > maxNow {
				now = maxNow
			}
			o.now = now
			if rng.Intn(10) == 0 {
				o.rtt = int64(1_000_000_001 + rng.Intn(1_000_000_000)) // 非法 rtt，两侧都应拒绝
			} else {
				o.rtt = int64(rng.Intn(1_000_000_001))
			}
			o.ok = rng.Intn(5) != 0
		}
		if o.badID {
			o.id = ""
		}
		log = append(log, o)

		var rErr, nErr error
		var rTk, nTk int64
		var rEp, nEp string
		switch o.kind {
		case 0:
			rErr = real.AddEndpoint(o.id)
			nErr = naive.add(o.id)
		case 1:
			rErr = real.RemoveEndpoint(o.id)
			nErr = naive.remove(o.id)
		case 2:
			rTk, rEp, rErr = real.Pick(o.now, o.r1, o.r2)
			nTk, nEp, nErr = naive.pick(o.now, o.r1, o.r2)
		case 3:
			rErr = real.Release(o.ticket, o.rtt, o.ok, o.now)
			nErr = naive.release(o.ticket, o.rtt, o.ok, o.now)
		}

		if !sameErr(rErr, nErr) {
			failf(s, "err mismatch real=%v naive=%v", rErr, nErr)
			return
		}
		if o.kind == 2 && rErr == nil {
			if rTk != nTk || rEp != nEp {
				failf(s, "pick mismatch real=(%d,%q) naive=(%d,%q)", rTk, rEp, nTk, nEp)
				return
			}
			openTickets = append(openTickets, rTk)
		}
		if o.kind == 3 && rErr == nil && o.ticket < int64(1<<40) {
			for i, tk := range openTickets {
				if tk == o.ticket {
					openTickets = append(openTickets[:i], openTickets[i+1:]...)
					break
				}
			}
		}

		// 全量快照对照（在当前单调时间点读 val）。
		rs := real.snap(o.now)
		ns := naive.snap(o.now)
		if !reflect.DeepEqual(rs, ns) {
			failf(s, "snapshot mismatch\nreal =%+v\nnaive=%+v", rs, ns)
			return
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Error() == b.Error()
}

func formatOps(ops []op) string {
	out := ""
	for i, o := range ops {
		switch o.kind {
		case 0:
			out += fmt.Sprintf("  [%d] Add(%q)\n", i, o.id)
		case 1:
			out += fmt.Sprintf("  [%d] Remove(%q)\n", i, o.id)
		case 2:
			out += fmt.Sprintf("  [%d] Pick(now=%d r1=%d r2=%d)\n", i, o.now, o.r1, o.r2)
		case 3:
			out += fmt.Sprintf("  [%d] Release(tk=%d rtt=%d ok=%v now=%d)\n",
				i, o.ticket, o.rtt, o.ok, o.now)
		}
	}
	return out
}
