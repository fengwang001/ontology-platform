package spectate

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/delay"
	"ontology/live"
)

type naiveViewer struct {
	judge  bool
	cursor int64
}

// naiveWorld 是逐事件扫描的朴素模型, 与真实 Service 逐步对照。
type naiveWorld struct {
	d, m     int64
	events   []Event
	ended    bool
	tE       int64
	mode     Mode
	friends  map[string]bool
	viewers  map[string]*naiveViewer
	nonJudge int
}

func newNaive(D int64, M int) *naiveWorld {
	return &naiveWorld{
		d:       D,
		m:       int64(M),
		friends: map[string]bool{},
		viewers: map[string]*naiveViewer{},
	}
}

func nCutoff(w *naiveWorld, now int64, judge bool) int64 {
	return delay.Cutoff(now, judge, delay.View{D: w.d, Ended: w.ended, TE: w.tE})
}

func nVisible(w *naiveWorld, e Event, now int64, judge bool) bool {
	c := nCutoff(w, now, judge)
	if e.Now > c {
		return false
	}
	if judge || e.Kind != live.Hidden {
		return true
	}
	return w.ended && c == w.tE
}

// nPull 按题面逐事件扫描, 返回投递 seq 列表与 More。
func nPull(w *naiveWorld, now int64, name string, maxN int) ([]int64, bool) {
	v := w.viewers[name]
	var got []int64
	full := false
	for seq := v.cursor + 1; int(seq) <= len(w.events); seq++ {
		e := w.events[seq-1]
		if e.Now > nCutoff(w, now, v.judge) {
			break
		}
		if nVisible(w, e, now, v.judge) {
			got = append(got, seq)
			v.cursor = seq
			if len(got) >= maxN {
				full = true
				break
			}
		} else {
			v.cursor = seq
		}
	}
	more := false
	if full {
		for seq := v.cursor + 1; int(seq) <= len(w.events); seq++ {
			if nVisible(w, w.events[seq-1], now, v.judge) {
				more = true
				break
			}
		}
	}
	return got, more
}

func nLag(w *naiveWorld, now int64, name string) int {
	v := w.viewers[name]
	n := 0
	for seq := v.cursor + 1; int(seq) <= len(w.events); seq++ {
		e := w.events[seq-1]
		if e.Now > nCutoff(w, now, v.judge) {
			break
		}
		if nVisible(w, e, now, v.judge) {
			n++
		}
	}
	return n
}

func nJoin(w *naiveWorld, name string, judge bool) error {
	if _, ok := w.viewers[name]; ok {
		return ErrAlreadyJoined
	}
	if !judge {
		if w.mode == Off {
			return ErrModeOff
		}
		if w.mode == FriendsOnly && !w.friends[name] {
			return ErrNotFriend
		}
		if int64(w.nonJudge) >= w.m {
			return ErrSpectatorLimit
		}
	}
	w.viewers[name] = &naiveViewer{judge: judge}
	if !judge {
		w.nonJudge++
	}
	return nil
}

func nLeave(w *naiveWorld, name string) error {
	v, ok := w.viewers[name]
	if !ok {
		return ErrNotSpectating
	}
	delete(w.viewers, name)
	if !v.judge {
		w.nonJudge--
	}
	return nil
}

func nSetMode(w *naiveWorld, mode Mode) {
	for name, v := range w.viewers {
		if v.judge {
			continue
		}
		if mode == Off || (mode == FriendsOnly && !w.friends[name]) {
			delete(w.viewers, name)
			w.nonJudge--
		}
	}
	w.mode = mode
}

func sameErr(a, b error) bool {
	return a == b || (a != nil && b != nil && a.Error() == b.Error())
}

// TestRandomDifferential 1500 组随机操作序列对照朴素模型。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for g := 0; g < 1500; g++ {
		runOneRandom(t, rng, g)
	}
}

func runOneRandom(t *testing.T, rng *rand.Rand, g int) {
	D := []int64{0, 1, 2, 3, 2999, 3000, 3001, 1_000_000_000}[rng.Intn(8)]
	M := []int{1, 1, 2, 3, 8}[rng.Intn(5)]
	svc, err := New(D, M)
	if err != nil {
		t.Fatal(err)
	}
	w := newNaive(D, M)
	nonJudgeNames := []string{"v0", "v1", "v2", "v3"}
	allNames := []string{"v0", "v1", "v2", "v3", "j0"}

	var logb strings.Builder
	fmt.Fprintf(&logb, "group %d D=%d M=%d\n", g, D, M)
	verbose := testing.Verbose() && g < 3
	failf := func(format string, a ...any) {
		t.Fatalf("%s\n判定依据: %s", logb.String(), fmt.Sprintf(format, a...))
	}
	trace := func(format string, a ...any) {
		if verbose {
			fmt.Fprintf(&logb, "  -> "+format+"\n", a...)
		}
	}

	now := int64(0)
	for step := 0; step < 120; step++ {
		now += int64(rng.Intn(5))
		switch rng.Intn(11) {
		case 0, 1: // Emit Normal/Hidden
			if w.ended {
				continue
			}
			kind := live.Normal
			if rng.Intn(3) == 0 {
				kind = live.Hidden
			}
			fmt.Fprintf(&logb, "step%d Emit(%d,%v)\n", step, now, kind)
			if _, err := svc.Emit(now, kind); err != nil {
				failf("Emit: %v", err)
			}
			w.events = append(w.events, Event{Seq: int64(len(w.events) + 1), Now: now, Kind: kind})
		case 2: // Emit End
			if w.ended {
				continue
			}
			fmt.Fprintf(&logb, "step%d Emit(%d,End)\n", step, now)
			if _, err := svc.Emit(now, live.End); err != nil {
				failf("Emit End: %v", err)
			}
			w.events = append(w.events, Event{Seq: int64(len(w.events) + 1), Now: now, Kind: live.End})
			w.ended = true
			w.tE = now
		case 3: // SetMode
			mode := []Mode{Public, FriendsOnly, Off}[rng.Intn(3)]
			fmt.Fprintf(&logb, "step%d SetMode(%d,%v)\n", step, now, mode)
			if err := svc.SetMode(now, mode); err != nil {
				failf("SetMode: %v", err)
			}
			nSetMode(w, mode)
		case 4, 5: // Befriend / Unfriend
			name := nonJudgeNames[rng.Intn(4)]
			if rng.Intn(2) == 0 {
				fmt.Fprintf(&logb, "step%d Befriend(%d,%s)\n", step, now, name)
				if err := svc.Befriend(now, name); err != nil {
					failf("Befriend: %v", err)
				}
				w.friends[name] = true
			} else {
				fmt.Fprintf(&logb, "step%d Unfriend(%d,%s)\n", step, now, name)
				if err := svc.Unfriend(now, name); err != nil {
					failf("Unfriend: %v", err)
				}
				delete(w.friends, name)
				if v, ok := w.viewers[name]; ok && !v.judge && w.mode == FriendsOnly {
					delete(w.viewers, name)
					w.nonJudge--
				}
			}
		case 6: // Join
			name := allNames[rng.Intn(5)]
			judge := name == "j0"
			fmt.Fprintf(&logb, "step%d Join(%d,%s,j=%v)\n", step, now, name, judge)
			errS := svc.Join(now, name, judge)
			errW := nJoin(w, name, judge)
			trace("Join svc=%v naive=%v", errS, errW)
			if !sameErr(errS, errW) {
				failf("Join(%s,j=%v): svc=%v naive=%v", name, judge, errS, errW)
			}
		case 7: // Leave
			name := nonJudgeNames[rng.Intn(4)]
			fmt.Fprintf(&logb, "step%d Leave(%d,%s)\n", step, now, name)
			errS := svc.Leave(now, name)
			errW := nLeave(w, name)
			trace("Leave svc=%v naive=%v", errS, errW)
			if !sameErr(errS, errW) {
				failf("Leave(%s): svc=%v naive=%v", name, errS, errW)
			}
		case 8: // Pull
			name := allNames[rng.Intn(5)]
			maxN := 1 + rng.Intn(6)
			fmt.Fprintf(&logb, "step%d Pull(%d,%s,n=%d)\n", step, now, name, maxN)
			res, touch, errS := svc.Pull(now, name, maxN)
			v, present := w.viewers[name]
			if !present {
				if errS != ErrNotSpectating {
					failf("Pull absent(%s): %v", name, errS)
				}
				continue
			}
			oldCursor := v.cursor
			if errS != nil {
				failf("Pull: %v", errS)
			}
			wantSeq, wantMore := nPull(w, now, name, maxN)
			trace("Pull svc=%v/more=%v naive=%v/more=%v touched=records%d/probes%d",
				seqs(res.Events), res.More, wantSeq, wantMore, touch.Records, touch.Probes)
			if !eqSeq(seqs(res.Events), wantSeq) || res.More != wantMore {
				failf("Pull(%s) svc=%v/more=%v naive=%v/more=%v",
					name, seqs(res.Events), res.More, wantSeq, wantMore)
			}
			cutoff := nCutoff(w, now, v.judge)
			var prev int64
			for _, e := range res.Events {
				if e.Seq <= prev {
					failf("seq not strictly increasing: %v", seqs(res.Events))
				}
				prev = e.Seq
				if e.Now > cutoff {
					failf("event t=%d > cutoff=%d leaked", e.Now, cutoff)
				}
				if !v.judge && e.Kind == live.Hidden && !(w.ended && cutoff == w.tE) {
					failf("hidden leaked at cutoff=%d tE=%d", cutoff, w.tE)
				}
			}
			// touched: 线性扫描读取记录数 <= 投递数 + 跳过数 + 1。
			// 跳过数即本次扫描游标越过、未投递的事件数; 由朴素扫描游标
			// 的净移动减去投递数得出(返回为空时游标仍可能越过 Hidden)。
			skipped := int(v.cursor-oldCursor) - len(res.Events)
			if touch.Records > len(res.Events)+skipped+1 {
				failf("Pull touched records=%d > delivered=%d+skipped=%d+1",
					touch.Records, len(res.Events), skipped)
			}
		default: // Lag
			name := allNames[rng.Intn(5)]
			fmt.Fprintf(&logb, "step%d Lag(%d,%s)\n", step, now, name)
			got, touch, errS := svc.Lag(now, name)
			if _, ok := w.viewers[name]; !ok {
				if errS != ErrNotSpectating {
					failf("Lag absent(%s): %v", name, errS)
				}
				continue
			}
			if errS != nil {
				failf("Lag: %v", err)
			}
			if want := nLag(w, now, name); got != want {
				failf("Lag(%s) svc=%d naive=%d", name, got, want)
			}
			trace("Lag svc=%d touched=records%d/probes%d", got, touch.Records, touch.Probes)
			if touch.Records > 0 || touch.Probes > 64 {
				failf("Lag reads records=%d probes=%d (need 0 records, <=64 probes)",
					touch.Records, touch.Probes)
			}
		}
		if w.nonJudge > M {
			failf("non-judge count %d > M %d", w.nonJudge, M)
		}
	}
	if verbose {
		t.Logf("%s", logb.String())
	}
}
