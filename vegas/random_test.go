package vegas

import (
	"fmt"
	"math/rand"
	"testing"
)

// genOps 用朴素模拟器驱动生成操作序列，同时刻意注入各类非法输入：
// 非法结果、非法时间、时钟回退（三类前置拒绝），以及无效令牌、0 rtt、
// 重复归还、超时归还等状态类情形。生成过程中 now 主体非降，
// 只有被设计为“回退”的操作携带更小时间（期望被拒绝）。
func genOps(r *rand.Rand, n int, c Config) ([]testOp, []naiveOutcome) {
	m := newNaive(c)
	var ops []testOp
	var want []naiveOutcome
	now := int64(0)
	var out []int64 // 朴素视角在途令牌序号

	step := func() int64 {
		now += int64(r.Intn(8))
		return now
	}
	for i := 0; i < n; i++ {
		roll := r.Intn(100)
		switch {
		case roll < 6:
			// 非法时间。
			t := step()
			bad := int64(-1)
			if r.Intn(2) == 0 {
				bad = maxTime + 1 + int64(r.Intn(10))
			}
			if r.Intn(2) == 0 {
				ops = append(ops, testOp{kind: 0, now: bad, desc: "bad-time acquire"})
				want = append(want, m.acquire(bad))
			} else {
				seq := int64(1 + r.Intn(3))
				ops = append(ops, testOp{kind: 1, now: bad, seq: seq, res: Ignored, noRtt: true, desc: "bad-time release"})
				want = append(want, m.release(seq, Ignored, 0, bad))
			}
			_ = t
		case roll < 12:
			// 时钟回退（在已推进的 maxNow 之上）。
			t := step()
			past := int64(0)
			if t > 0 {
				past = t - 1 - int64(r.Intn(int(t)))
				if past < 0 {
					past = 0
				}
			}
			ops = append(ops, testOp{kind: 0, now: past, desc: "rewind acquire"})
			want = append(want, m.acquire(past))
		case roll < 18:
			// 非法结果（Release 专属，优先级最高）。
			t := step()
			seq := int64(1 + r.Intn(3))
			ops = append(ops, testOp{kind: 1, now: t, seq: seq, res: Result(7 + r.Intn(10)), rtt: 0, noRtt: true, desc: "bad-result"})
			want = append(want, m.release(seq, Result(7+r.Intn(10)), 0, t))
		case roll < 52:
			t := step()
			ops = append(ops, testOp{kind: 0, now: t, desc: "acquire"})
			o := m.acquire(t)
			want = append(want, o)
			if o.ok {
				out = append(out, o.seq)
			}
		default:
			t := step()
			if len(out) == 0 {
				// 没有在途令牌：尝试无效/怪异序号。
				var seq int64
				switch r.Intn(3) {
				case 0:
					seq = 0
				case 1:
					seq = m.nextSeq + int64(r.Intn(5))
				default:
					seq = int64(1 + r.Intn(3))
				}
				res := validResults[r.Intn(3)]
				rtt := int64(1 + r.Intn(int(maxRTT)))
				ops = append(ops, testOp{kind: 1, now: t, seq: seq, res: res, rtt: rtt, desc: "release invalid token"})
				want = append(want, m.release(seq, res, rtt, t))
				continue
			}
			idx := r.Intn(len(out))
			seq := out[idx]
			res := validResults[r.Intn(3)]
			rtt := int64(1 + r.Intn(2000))
			// 注入 rtt=0 的成功（状态类拒绝，令牌保留）。
			if res == Success && r.Intn(8) == 0 {
				rtt = 0
			}
			// 偶尔故意延迟归还，使其越过超时，验证“已超时”。
			ops = append(ops, testOp{kind: 1, now: t, seq: seq, res: res, rtt: rtt, desc: "release"})
			o := m.release(seq, res, rtt, t)
			want = append(want, o)
			if o.ok {
				out = append(out[:idx], out[idx+1:]...)
			} else if o.err == ErrTokenTimedOut {
				out = append(out[:idx], out[idx+1:]...)
			}
		}
	}
	return ops, want
}

func fastOutcome(l *Limiter, op testOp) naiveOutcome {
	if op.kind == 0 {
		tk, err := l.Acquire(op.now)
		if err != nil {
			return naiveOutcome{err: err, l: l.L(), n: l.N(), mr: l.MinRTT()}
		}
		return naiveOutcome{ok: true, seq: tk.Seq, w: tk.W, exp: tk.ExpiresAt, l: l.L(), n: l.N(), mr: l.MinRTT()}
	}
	rtt := op.rtt
	err := l.Release(op.seq, op.res, rtt, op.now)
	return naiveOutcome{ok: err == nil, err: err, l: l.L(), n: l.N(), mr: l.MinRTT()}
}

func TestAgainstNaiveRandom(t *testing.T) {
	const groups, opsPerGroup = 2000, 80
	codeHits := map[string]int{}
	for g := 0; g < groups; g++ {
		r := rand.New(rand.NewSource(int64(g + 1)))
		c := randConfig(r)
		ops, want := genOps(r, opsPerGroup, c)
		fast, err := New(c)
		if err != nil {
			t.Fatalf("group %d config %+v: %v", g, c, err)
		}
		for i, op := range ops {
			got := fastOutcome(fast, op)
			w := want[i]
			codeHits[errCode(w.err)]++
			compareM := op.kind == 1 && op.res == Success && w.ok
			if errCode(got.err) != errCode(w.err) ||
				got.l != w.l || got.n != w.n || (compareM && got.mr != w.mr) {
				t.Fatalf("group %d op %d [%s] %+v\n want err=%s L=%d n=%d m=%d\n got  err=%s L=%d n=%d m=%d\nconfig=%+v",
					g, i, op.desc, op,
					errCode(w.err), w.l, w.n, w.mr,
					errCode(got.err), got.l, got.n, got.mr, c)
			}
			if w.ok && op.kind == 0 && (got.seq != w.seq || got.w != w.w || got.exp != w.exp) {
				t.Fatalf("group %d op %d token mismatch want {s=%d w=%d e=%d} got {s=%d w=%d e=%d}",
					g, i, w.seq, w.w, w.exp, got.seq, got.w, got.exp)
			}
		}
		if testing.Verbose() && g < 3 {
			fmt.Printf("group %d config=%+v final L=%d n=%d m=%d\n", g, c, fast.L(), fast.N(), fast.MinRTT())
		}
	}
	for _, code := range []string{"bad-result", "bad-time", "rewind", "capacity", "timed-out", "bad-token", "bad-rtt"} {
		if codeHits[code] == 0 {
			t.Fatalf("random suite never produced rejection %q (hits=%v)", code, codeHits)
		}
	}
	t.Logf("rejection coverage over %d groups: %v", groups, codeHits)
}
