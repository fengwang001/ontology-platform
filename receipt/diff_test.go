package receipt

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

func errClass(err error) string {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrMessageNotFound):
		return "nomsg"
	case errors.Is(err, ErrUnknownRcpt):
		return "norcpt"
	case errors.Is(err, ErrClockSkew):
		return "skew"
	case err == nil:
		return ""
	default:
		return err.Error()
	}
}

// 朴素模型：严格按题目文字逐条重写，不与生产实现共享任何决策代码。

type simRcpt struct {
	r       int
	fail    Fail
	sentA   int
	softSet map[int]bool
	seen    map[receiptKey]bool
	late    bool
}

type simMsg struct {
	rcpts    map[string]*simRcpt
	order    []string
	deadline int64
}

type simTracker struct {
	s        int
	msgs     map[string]*simMsg
	lastTick int64
}

func newSim(s int) *simTracker {
	return &simTracker{s: s, msgs: map[string]*simMsg{}}
}

var simKinds = map[string]int{"sent": 1, "delivered": 2, "read": 3}

// simReceipt 严格按处置次序执行，reason 记录判定依据。
func (s *simTracker) simReceipt(msg, rcpt, kind string, attempt int, ts int64) (ReceiptOutcome, string) {
	if _, ok := simKinds[kind]; !ok && kind != "soft" && kind != "hard" {
		return 0, "invalid"
	}
	if attempt < 1 || attempt > 16 || ts < 0 || ts > maxTS {
		return 0, "invalid"
	}
	m, ok := s.msgs[msg]
	if !ok {
		return 0, "nomsg"
	}
	r, ok := m.rcpts[rcpt]
	if !ok {
		return 0, "norcpt"
	}
	k := receiptKey{kind, attempt}
	if r.seen[k] {
		return Duplicate, "(kind,attempt) already in seen -> Duplicate, state unchanged"
	}
	r.seen[k] = true

	if p, isProgress := simKinds[kind]; isProgress {
		switch r.fail {
		case FailSoft, FailHard:
			return Ignored, fmt.Sprintf("fail=%s terminal -> progress Ignored", r.fail)
		case FailExpired:
			if (kind == "delivered" || kind == "read") && ts < m.deadline {
				r.fail = FailNone
				if p > r.r {
					r.r = p
				}
				return Applied, fmt.Sprintf("exp revoked by %s ts=%d < deadline=%d -> r=max -> Applied", kind, ts, m.deadline)
			}
			return Ignored, "exp: revoke needs delivered/read with ts strictly < deadline"
		}
		if kind == "sent" && attempt > r.sentA {
			r.sentA = attempt
		}
		if p > r.r {
			r.r = p
			return Applied, fmt.Sprintf("fail=none: sentA/max logic, r %d->%d Applied", r.r, p)
		}
		return Stale, "fail=none but r did not increase -> Stale (sentA still advanced if sent)"
	}

	if kind == "soft" {
		if r.fail != FailNone {
			return Ignored, fmt.Sprintf("soft with fail=%s -> Ignored", r.fail)
		}
		if r.r >= 2 {
			return Stale, "soft while r>=2 -> Stale, not recorded"
		}
		r.softSet[attempt] = true
		cnt := 0
		for a := range r.softSet {
			if a >= r.sentA {
				cnt++
			}
		}
		if cnt >= s.s {
			r.fail = FailSoft
			return Applied, fmt.Sprintf("soft recorded; cnt(>=sentA=%d)=%d >= S=%d -> fail=soft Applied", r.sentA, cnt, s.s)
		}
		return Applied, fmt.Sprintf("soft recorded; cnt(>=sentA=%d)=%d < S=%d Applied, no fail yet", r.sentA, cnt, s.s)
	}

	// hard
	if r.fail == FailSoft || r.fail == FailHard {
		return Ignored, fmt.Sprintf("hard with fail=%s -> Ignored", r.fail)
	}
	if r.r >= 2 {
		r.late = true
		return Stale, "hard while r>=2 -> late=true, Stale"
	}
	r.fail = FailHard
	return Applied, fmt.Sprintf("hard while r<2 (fail=%s) -> fail=hard Applied", FailExpired)
}

func (s *simTracker) simTick(now int64) ([][2]string, string, string) {
	if now < 0 || now > maxTS {
		return nil, "invalid", "now out of range"
	}
	if now < s.lastTick {
		return nil, "skew", "now before last tick"
	}
	s.lastTick = now
	var out [][2]string
	for name, m := range s.msgs {
		if m.deadline > now {
			continue
		}
		for _, rc := range m.order {
			r := m.rcpts[rc]
			if r.fail == FailNone && r.r < 2 {
				r.fail = FailExpired
				out = append(out, [2]string{name, rc})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out, "", fmt.Sprintf("deadline<=%d newly expired -> exp", now)
}

type rcptSnap struct {
	r       int
	fail    Fail
	sentA   int
	late    bool
	softSet []int
	seen    []receiptKey
}

func snapSet(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func snapSeen(m map[receiptKey]bool) []receiptKey {
	var out []receiptKey
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].kind != out[j].kind {
			return out[i].kind < out[j].kind
		}
		return out[i].attempt < out[j].attempt
	})
	return out
}

func snapReal(tr *Tracker) map[string]map[string]rcptSnap {
	out := map[string]map[string]rcptSnap{}
	for mk, m := range tr.msgs {
		rs := map[string]rcptSnap{}
		for name, r := range m.rcpts {
			rs[name] = rcptSnap{r.r, r.fail, r.sentA, r.late, snapSet(r.softSet), snapSeen(r.seen)}
		}
		out[mk] = rs
	}
	return out
}

func snapSim(s *simTracker) map[string]map[string]rcptSnap {
	out := map[string]map[string]rcptSnap{}
	for mk, m := range s.msgs {
		rs := map[string]rcptSnap{}
		for name, r := range m.rcpts {
			rs[name] = rcptSnap{r.r, r.fail, r.sentA, r.late, snapSet(r.softSet), snapSeen(r.seen)}
		}
		out[mk] = rs
	}
	return out
}

func fmtSnap(m map[string]map[string]rcptSnap) string {
	msgs := make([]string, 0, len(m))
	for k := range m {
		msgs = append(msgs, k)
	}
	sort.Strings(msgs)
	var b strings.Builder
	for _, mk := range msgs {
		rcpts := make([]string, 0, len(m[mk]))
		for k := range m[mk] {
			rcpts = append(rcpts, k)
		}
		sort.Strings(rcpts)
		for _, rk := range rcpts {
			s := m[mk][rk]
			fmt.Fprintf(&b, "\n    %s/%s r=%d fail=%s sentA=%d late=%v soft=%v seen=%v",
				mk, rk, s.r, s.fail, s.sentA, s.late, s.softSet, s.seen)
		}
	}
	return b.String()
}

// TestDifferential2000：2000 组随机操作序列，生产实现与朴素模型逐步对拍。
func TestDifferential2000(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential fuzz in -short mode")
	}
	logFile, err := os.Create("fuzz_trace.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	const cases = 2000
	for c := 0; c < cases; c++ {
		rng := rand.New(rand.NewSource(int64(c) + 1))
		s := 1 + rng.Intn(4)
		tr, err := NewTracker(s)
		if err != nil {
			t.Fatal(err)
		}
		sim := newSim(s)

		nRcpt := 1 + rng.Intn(4)
		rcpts := make([]string, nRcpt)
		rcptBytes := make([][]byte, nRcpt)
		for i := range rcpts {
			rcpts[i] = fmt.Sprintf("r%d", i)
			rcptBytes[i] = []byte(rcpts[i])
		}
		deadline := int64(rng.Intn(60))
		msg := fmt.Sprintf("msg%d", c)
		if err := tr.Send([]byte(msg), rcptBytes, deadline); err != nil {
			t.Fatal(err)
		}
		sim.msgs[msg] = &simMsg{
			rcpts:    map[string]*simRcpt{},
			deadline: deadline,
			order:    append([]string(nil), rcpts...),
		}
		for _, name := range rcpts {
			sim.msgs[msg].rcpts[name] = &simRcpt{softSet: map[int]bool{}, seen: map[receiptKey]bool{}}
		}

		var trace strings.Builder
		fmt.Fprintf(&trace, "case %d seed=%d S=%d rcpts=%v deadline=%d", c+1, c+1, s, rcpts, deadline)
		var simTickNow int64
		steps := 30 + rng.Intn(20)
		failedCase := false
		for step := 0; step < steps; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5, 6: // 回执
				rcpt := rcpts[rng.Intn(nRcpt)]
				kind := []string{"sent", "delivered", "read", "soft", "hard"}[rng.Intn(5)]
				attempt := 1 + rng.Intn(5)
				ts := int64(rng.Intn(70))
				got, gerr := tr.Receipt([]byte(msg), []byte(rcpt), kind, attempt, ts)
				want, reason := sim.simReceipt(msg, rcpt, kind, attempt, ts)
				in := fmt.Sprintf("Receipt(%s,%s,%s,a=%d,ts=%d)", msg, rcpt, kind, attempt, ts)
				gclass := errClass(gerr)
				wclass := ""
				if _, isOutcome := map[ReceiptOutcome]bool{Applied: true, Stale: true, Duplicate: true, Ignored: true}[want]; !isOutcome {
					wclass = reason
				}
				fmt.Fprintf(&trace, "\n  %s -> real=%s[%s] sim=%s[%s] | %s", in, got, gclass, want, wclass, reason)
				if gclass != wclass || (gclass == "" && got != want) {
					fmt.Fprintf(&trace, "\n  *** MISMATCH ***")
					failedCase = true
				}
			case 7, 8: // Tick
				var now int64
				if rng.Intn(6) == 0 {
					now = simTickNow - 1 - int64(rng.Intn(3)) // 故意回退
					if now < 0 {
						now = 0
					}
				} else {
					now = int64(rng.Intn(70))
					if now < simTickNow {
						now = simTickNow
					}
					simTickNow = now
				}
				got, gerr := tr.Tick(now)
				want, wclass, reason := sim.simTick(now)
				gclass := errClass(gerr)
				fmt.Fprintf(&trace, "\n  Tick(%d) -> real=%v[%s] sim=%v[%s] | %s", now, got, gclass, want, wclass, reason)
				if gclass != wclass || (gclass == "" && fmt.Sprint(got) != fmt.Sprint(want)) {
					fmt.Fprintf(&trace, "\n  *** MISMATCH ***")
					failedCase = true
				}
			case 9: // Status 汇总口径
				got, gerr := tr.Status([]byte(msg))
				if gerr != nil {
					fmt.Fprintf(&trace, "\n  Status -> err %v", gerr)
					failedCase = true
				} else {
					fmt.Fprintf(&trace, "\n  Status -> summary=%s", got.Summary)
				}
			}
			if snapEq := fmtSnap(snapReal(tr)) == fmtSnap(snapSim(sim)); !snapEq {
				fmt.Fprintf(&trace, "\n  *** STATE MISMATCH *** real:%s\n  sim:%s",
					fmtSnap(snapReal(tr)), fmtSnap(snapSim(sim)))
				failedCase = true
				break
			}
		}
		if tr.lastTick != sim.lastTick {
			fmt.Fprintf(&trace, "\n  *** lastTick mismatch real=%d sim=%d", tr.lastTick, sim.lastTick)
			failedCase = true
		}
		if failedCase {
			t.Fatalf("case %d mismatch; trace:\n%s\nreal:%s\nsim:%s",
				c+1, trace.String(), fmtSnap(snapReal(tr)), fmtSnap(snapSim(sim)))
		}
		if c < 10 || (c+1)%500 == 0 {
			fmt.Fprintf(logFile, "%s\n", trace.String())
		}
	}
	t.Logf("differential fuzz: %d cases matched the naive model; sample traces in receipt/fuzz_trace.log", cases)
}
