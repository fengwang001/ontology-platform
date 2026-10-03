package flexray

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type opKind int

const (
	opAssign opKind = iota
	opPost
	opCycle
	opPending
	opStats
)

type simOp struct {
	kind   opKind
	id     int
	base   int
	rep    int
	length int
	tag    string
}

var repChoices = []int{1, 2, 4, 8, 16, 32, 64}

func (o simOp) String() string {
	switch o.kind {
	case opAssign:
		return fmt.Sprintf("Assign(id=%d, base=%d, rep=%d)", o.id, o.base, o.rep)
	case opPost:
		return fmt.Sprintf("Post(id=%d, L=%d, tag=%q)", o.id, o.length, o.tag)
	case opCycle:
		return "Cycle()"
	case opPending:
		return fmt.Sprintf("Pending(id=%d)", o.id)
	default:
		return "Stats()"
	}
}

func cycleEqual(g, w CycleResult) bool {
	if g.C != w.C || g.Unused != w.Unused || g.EmptyFrame != w.EmptyFrame ||
		len(g.Sent) != len(w.Sent) {
		return false
	}
	for i := range g.Sent {
		if g.Sent[i] != w.Sent[i] {
			return false
		}
	}
	return true
}

// checkDynInvariants 用朴素模拟器记录的发送长度，检查动态区间落在 1..N、
// 起点不迟于 Lt，且相邻占用区间互不重叠。
func checkDynInvariants(t *testing.T, r CycleResult, s *naiveSim, seed int64, step int, log *strings.Builder) {
	t.Helper()
	N := s.nm + r.Unused
	li := 0
	prevEnd := 0
	for _, item := range r.Sent {
		if item.ID <= s.ns {
			if item.Slot != item.ID || item.Start != 0 {
				t.Fatalf("seed %d step %d bad static item %+v\n%s", seed, step, item, log.String())
			}
			continue
		}
		if li >= len(s.lastDynL) {
			t.Fatalf("seed %d step %d missing length record\n%s", seed, step, log.String())
		}
		L := s.lastDynL[li]
		li++
		if item.Start < 1 || item.Start > s.lt || item.Start+L-1 > N {
			t.Fatalf("seed %d step %d frame %d interval [%d,%d] violates Lt=%d N=%d\n%s",
				seed, step, item.ID, item.Start, item.Start+L-1, s.lt, N, log.String())
		}
		if item.Start <= prevEnd {
			t.Fatalf("seed %d step %d overlap at frame %d start=%d prevEnd=%d\n%s",
				seed, step, item.ID, item.Start, prevEnd, log.String())
		}
		prevEnd = item.Start + L - 1
	}
}

// TestNaiveDifferential 2000 组随机操作序列，与按规则逐步书写的朴素模拟器对拍。
// 日志记录每条输入、双方输出及判定依据；-v 时逐条打印，失配时全部打印。
func TestNaiveDifferential(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ns := 1 + rng.Intn(5)
		nm := rng.Intn(9)
		lt := rng.Intn(nm + 1)

		a, err := New(ns, nm, lt)
		s, code := newNaiveSim(ns, nm, lt)
		if err != nil || code != 0 {
			t.Fatalf("seed %d construct: err=%v simCode=%v", seed, err, code)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d New(%d,%d,%d)\n", seed, ns, nm, lt)
		total := ns + nm

		for step := 0; step < 120; step++ {
			o := simOp{}
			switch rng.Intn(10) {
			case 0, 1:
				o.kind = opAssign
				o.id = 1 + rng.Intn(total+2)
				if rng.Intn(8) == 0 {
					o.rep = 3
					o.base = rng.Intn(4)
				} else {
					o.rep = repChoices[rng.Intn(len(repChoices))]
					o.base = rng.Intn(o.rep + 1)
				}
			case 2, 3, 4, 5:
				o.kind = opPost
				o.id = 1 + rng.Intn(total+2)
				if o.id > ns && o.id <= total {
					o.length = 1 + rng.Intn(nm)
					if rng.Intn(8) == 0 {
						o.length = nm + 1
					}
				} else {
					o.length = 1 + rng.Intn(nm+2)
				}
				o.tag = fmt.Sprintf("s%d-%d", seed, step)
			case 6, 7:
				o.kind = opCycle
			case 8:
				o.kind = opPending
				o.id = 1 + rng.Intn(total+2)
			default:
				o.kind = opStats
			}
			fmt.Fprintf(&log, "step %3d: %s\n", step, o.String())

			switch o.kind {
			case opAssign:
				got := errCodeOf(a.Assign(o.id, o.base, o.rep))
				want := s.assign(o.id, o.base, o.rep)
				fmt.Fprintf(&log, "  -> code got=%d want=%d %s\n", got, want, matchMark(got == want))
				if got != want {
					t.Fatalf("seed %d step %d Assign mismatch\n%s", seed, step, log.String())
				}
			case opPost:
				got := errCodeOf(a.Post(o.id, o.length, o.tag))
				want := s.post(o.id, o.length, o.tag)
				fmt.Fprintf(&log, "  -> code got=%d want=%d %s\n", got, want, matchMark(got == want))
				if got != want {
					t.Fatalf("seed %d step %d Post mismatch\n%s", seed, step, log.String())
				}
			case opCycle:
				gr := a.Cycle()
				wr := s.cycle()
				ok := cycleEqual(gr, wr)
				fmt.Fprintf(&log, "  -> got {c:%d u:%d empty:%d sent:%v}\n", gr.C, gr.Unused, gr.EmptyFrame, gr.Sent)
				fmt.Fprintf(&log, "     want{c:%d u:%d empty:%d sent:%v} %s\n", wr.C, wr.Unused, wr.EmptyFrame, wr.Sent, matchMark(ok))
				if !ok {
					t.Fatalf("seed %d step %d cycle mismatch\n%s", seed, step, log.String())
				}
				checkDynInvariants(t, gr, s, seed, step, &log)
			case opPending:
				gt, ge := a.Pending(o.id)
				wt, we := s.pending(o.id)
				ok := errCodeOf(ge) == we && fmt.Sprint(gt) == fmt.Sprint(wt)
				fmt.Fprintf(&log, "  -> got tags=%v code=%d; want tags=%v code=%d %s\n",
					gt, errCodeOf(ge), wt, we, matchMark(ok))
				if !ok {
					t.Fatalf("seed %d step %d Pending mismatch\n%s", seed, step, log.String())
				}
			case opStats:
				gst := a.Stats()
				wst := s.stats()
				ok := gst == wst && a.C() == s.c
				fmt.Fprintf(&log, "  -> got stats=%+v c=%d; want stats=%+v c=%d %s\n",
					gst, a.C(), wst, s.c, matchMark(ok))
				if !ok {
					t.Fatalf("seed %d step %d stats mismatch\n%s", seed, step, log.String())
				}
			}

			if a.C() != s.c {
				t.Fatalf("seed %d step %d c mismatch: %d vs %d\n%s", seed, step, a.C(), s.c, log.String())
			}
		}

		for id := 1; id <= total; id++ {
			tags, _ := a.Pending(id)
			if len(tags) > 8 {
				t.Fatalf("seed %d queue %d len %d > 8", seed, id, len(tags))
			}
		}
		if testing.Verbose() {
			t.Logf("seed %d (%d,%d,%d) matched naive sim over 120 ops; stats=%+v\n%s",
				seed, ns, nm, lt, s.stats(), log.String())
		}
	}
	t.Logf("differential fuzz: %d random operation sequences all matched the naive simulation", sequences)
}

func matchMark(ok bool) string {
	if ok {
		return "[MATCH]"
	}
	return "[DIFF]"
}
