package resume_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"testing"

	"ontology/resume"
)

// ---- 朴素模型：保存全部帧、逐帧累加字节数、独立按规格实现 ----

type naiveSession struct {
	online bool
	gen    int
	ack    int64
	discAt int64
}

type naive struct {
	p, k, c int
	g       int64
	n       int
	frames  []uint64 // 1-based，下标 0 占位
	snapT   int64
	snapSz  uint64
	haveS   bool
	maxNow  int64
	players map[string]*naiveSession
}

func newNaive(p, k, c int, g int64, n int) *naive {
	return &naive{p: p, k: k, c: c, g: g, n: n,
		frames: []uint64{0}, players: map[string]*naiveSession{}}
}

func (m *naive) cur() int64 { return int64(len(m.frames) - 1) }

func (m *naive) low() int64 {
	lo := m.cur() - int64(m.k) + 1
	if lo < 1 {
		return 1
	}
	return lo
}

func (m *naive) present(s *naiveSession, now int64) bool {
	return s.online || now < s.discAt+m.g
}

// windowSum 逐帧累加 [from,to]，朴素实现。
func (m *naive) windowSum(from, to int64) uint64 {
	var total uint64
	for i := from; i <= to; i++ {
		total += m.frames[i]
	}
	return total
}

type opKind int

const (
	opAppend opKind = iota
	opJoin
	opAck
	opDisconnect
	opReconnect
)

type op struct {
	kind   opKind
	now    int64
	player string
	size   uint64
	num    int64 // Ack 的 a 或 Reconnect 的 have
	token  int
}

type outcome struct {
	err  error
	plan resume.Plan
	tok  int
}

func validClock(now int64) bool { return now >= 0 && now <= resume.MaxNow }

func (m *naive) planFor(have int64) resume.Plan {
	cur := m.cur()
	plan := resume.Plan{From: have + 1, To: cur}
	if have == cur {
		plan.Kind = resume.None
		return plan
	}
	n1 := cur - have
	buffered := have+1 >= m.low()
	cheap := !m.haveS || have >= m.snapT || n1 <= cur-m.snapT+int64(m.c)
	if buffered && cheap {
		plan.Kind = resume.Delta
		plan.Bytes = m.windowSum(have+1, cur)
		return plan
	}
	plan.Kind = resume.SnapshotKind
	plan.SnapTick = m.snapT
	plan.From = m.snapT + 1
	plan.To = cur
	plan.Bytes = m.windowSum(plan.From, cur) + m.snapSz
	return plan
}

func (m *naive) run(o op) outcome {
	switch o.kind {
	case opAppend:
		if !validClock(o.now) || o.size > resume.MaxSize {
			return outcome{err: resume.ErrInvalid}
		}
		if o.now < m.maxNow {
			return outcome{err: resume.ErrClock}
		}
		tick := m.cur() + 1
		m.frames = append(m.frames, o.size)
		m.maxNow = o.now
		if tick%int64(m.p) == 0 {
			m.snapT = tick
			m.snapSz = m.windowSum(tick-int64(m.p)+1, tick)
			m.haveS = true
		}
		return outcome{}
	case opJoin:
		if !validClock(o.now) || o.player == "" {
			return outcome{err: resume.ErrInvalid}
		}
		if o.now < m.maxNow {
			return outcome{err: resume.ErrClock}
		}
		s := m.players[o.player]
		if s != nil && m.present(s, o.now) {
			return outcome{err: resume.ErrState}
		}
		count := 0
		for _, other := range m.players {
			if other != s && m.present(other, o.now) {
				count++
			}
		}
		if count >= m.n {
			return outcome{err: resume.ErrFull}
		}
		if s == nil {
			s = &naiveSession{}
			m.players[o.player] = s
		} else {
			s.gen++
		}
		s.online = true
		s.ack = 0
		s.discAt = 0
		m.maxNow = o.now
		return outcome{}
	case opAck:
		if !validClock(o.now) || o.player == "" || o.num < 0 {
			return outcome{err: resume.ErrInvalid}
		}
		if o.now < m.maxNow {
			return outcome{err: resume.ErrClock}
		}
		s := m.players[o.player]
		if s == nil {
			return outcome{err: resume.ErrNotFound}
		}
		if !s.online {
			return outcome{err: resume.ErrState}
		}
		if o.num > m.cur() {
			return outcome{err: resume.ErrAhead}
		}
		m.maxNow = o.now
		if o.num >= s.ack {
			s.ack = o.num
		}
		return outcome{}
	case opDisconnect:
		if !validClock(o.now) || o.player == "" {
			return outcome{err: resume.ErrInvalid}
		}
		if o.now < m.maxNow {
			return outcome{err: resume.ErrClock}
		}
		s := m.players[o.player]
		if s == nil {
			return outcome{err: resume.ErrNotFound}
		}
		if !s.online {
			return outcome{err: resume.ErrState}
		}
		s.online = false
		s.discAt = o.now
		m.maxNow = o.now
		return outcome{tok: s.gen}
	case opReconnect:
		if !validClock(o.now) || o.player == "" || o.num < 0 {
			return outcome{err: resume.ErrInvalid}
		}
		if o.now < m.maxNow {
			return outcome{err: resume.ErrClock}
		}
		s := m.players[o.player]
		if s == nil {
			return outcome{err: resume.ErrNotFound}
		}
		if s.online || !m.present(s, o.now) {
			return outcome{err: resume.ErrState}
		}
		if o.token != s.gen {
			return outcome{err: resume.ErrToken}
		}
		if o.num > m.cur() {
			return outcome{err: resume.ErrAhead}
		}
		s.online = true
		s.discAt = 0
		s.gen++
		s.ack = o.num
		m.maxNow = o.now
		return outcome{plan: m.planFor(o.num)}
	}
	return outcome{}
}

type realModel struct{ pl *resume.Planner }

func (r realModel) run(o op) outcome {
	switch o.kind {
	case opAppend:
		return outcome{err: r.pl.Append(o.now, o.size)}
	case opJoin:
		return outcome{err: r.pl.Join(o.now, o.player)}
	case opAck:
		return outcome{err: r.pl.Ack(o.now, o.player, o.num)}
	case opDisconnect:
		tok, err := r.pl.Disconnect(o.now, o.player)
		return outcome{tok: tok, err: err}
	case opReconnect:
		plan, err := r.pl.Reconnect(o.now, o.player, o.token, o.num)
		return outcome{plan: plan, err: err}
	}
	return outcome{}
}

func describe(o op) string {
	switch o.kind {
	case opAppend:
		return fmt.Sprintf("%s(now=%d,size=%d)", "Append", o.now, o.size)
	case opJoin:
		return fmt.Sprintf("Join(now=%d,player=%q)", o.now, o.player)
	case opDisconnect:
		return fmt.Sprintf("Disconnect(now=%d,player=%q)", o.now, o.player)
	case opAck:
		return fmt.Sprintf("Ack(now=%d,player=%q,a=%d)", o.now, o.player, o.num)
	default:
		return fmt.Sprintf("Reconnect(now=%d,player=%q,token=%d,have=%d)",
			o.now, o.player, o.token, o.num)
	}
}

func describeOut(oc outcome) string {
	if oc.err != nil {
		return "err=" + oc.err.Error()
	}
	switch {
	case oc.plan.Kind == resume.SnapshotKind:
		return fmt.Sprintf("Snapshot(%d)+[%d,%d] bytes=%d",
			oc.plan.SnapTick, oc.plan.From, oc.plan.To, oc.plan.Bytes)
	case oc.plan.Kind == resume.Delta:
		return fmt.Sprintf("Delta[%d,%d] bytes=%d",
			oc.plan.From, oc.plan.To, oc.plan.Bytes)
	case oc.tok != 0 || oc.plan.To != 0:
		return fmt.Sprintf("tok=%d plan=None", oc.tok)
	default:
		return "ok"
	}
}

// TestRandomVsNaive：1500 组随机操作序列与朴素模型逐操作对拍；
// 日志打印输入、输出与判定依据，并在末尾重放同序列验证确定性。
func TestRandomVsNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq*7919 + 17)))
		p := 1 + rng.Intn(20)
		k := p + rng.Intn(40)
		c := rng.Intn(10)
		g := int64(1 + rng.Intn(100))
		n := 1 + rng.Intn(4)

		runSeq := func() ([]string, []op, []outcome, *resume.Planner) {
			pl := resume.New(p, k, c, g, n)
			model := newNaive(p, k, c, g, n)
			lastToken := map[string]int{}
			var ops []op
			var results []outcome
			var log []string
			steps := 120 + rng.Intn(120)
			now := int64(0)
			for step := 0; step < steps; step++ {
				now += int64(rng.Intn(int(g) + 40))
				player := "p" + strconv.Itoa(rng.Intn(n+2))
				o := op{kind: opKind(rng.Intn(5)), now: now, player: player}
				bad := rng.Intn(12) == 0
				switch o.kind {
				case opAppend:
					o.size = uint64(rng.Intn(5))
					if bad {
						o.size = resume.MaxSize + 1
					}
				case opAck:
					o.num = rng.Int63n(model.cur() + 3)
					if bad {
						o.num = -1
					}
				case opReconnect:
					o.num = rng.Int63n(model.cur() + 3)
					if rng.Intn(3) == 0 {
						o.token = lastToken[player]
					} else {
						o.token = rng.Intn(5) - 1
					}
					if bad {
						o.num = -1
					}
				}
				if bad && (o.kind == opJoin || o.kind == opDisconnect) {
					o.player = ""
				}
				if rng.Intn(15) == 0 {
					o.now = -1
				}
				ops = append(ops, o)

				got := realModel{pl}.run(o)
				want := model.run(o)
				results = append(results, got)
				if got.tok != want.tok {
					t.Fatalf("seq=%d(P=%d K=%d C=%d G=%d N=%d) step=%d %s token: got=%d want=%d",
						seq, p, k, c, g, n, step, describe(o), got.tok, want.tok)
				}
				if !sameErr(got.err, want.err) {
					t.Fatalf("seq=%d(P=%d K=%d C=%d G=%d N=%d) step=%d %s err: got=%v want=%v basis=%q",
						seq, p, k, c, g, n, step, describe(o), got.err, want.err, pl.LastReason())
				}
				if got.plan != want.plan {
					t.Fatalf("seq=%d step=%d %s plan:\n got=%+v\nwant=%+v basis=%q",
						seq, step, describe(o), got.plan, want.plan, pl.LastReason())
				}
				if got.err == nil {
					switch o.kind {
					case opDisconnect:
						lastToken[player] = got.tok
					case opReconnect:
						lastToken[player]++ // 成功重连后当前 gen 加 1
						if pl.Touched() > 2 {
							t.Fatalf("seq=%d step=%d touched=%d > 2", seq, step, pl.Touched())
						}
					}
					if pl.Cur() != model.cur() || pl.Low() != model.low() {
						t.Fatalf("seq=%d step=%d ring: got(%d,%d) want(%d,%d)",
							seq, step, pl.Cur(), pl.Low(), model.cur(), model.low())
					}
					present := 0
					for _, s := range model.players {
						if model.present(s, o.now) {
							present++
						}
					}
					if present > n {
						t.Fatalf("seq=%d step=%d present=%d > N=%d", seq, step, present, n)
					}
				}
				log = append(log, fmt.Sprintf("  %s -> %s | %s",
					describe(o), describeOut(got), pl.LastReason()))
			}
			return log, ops, results, pl
		}

		log, ops, results, pl1 := runSeq()
		// 确定性：相同操作序列在全新实例上重放，输出必须完全一致。
		plRe := resume.New(p, k, c, g, n)
		naiveRe := newNaive(p, k, c, g, n)
		for i, o := range ops {
			got := realModel{plRe}.run(o)
			want := naiveRe.run(o)
			if got != results[i] || !sameErr(want.err, got.err) || want.plan != got.plan || want.tok != got.tok {
				t.Fatalf("seq=%d replay mismatch at step=%d: first=%+v replay=%+v",
					seq, i, results[i], got)
			}
		}
		if len(ops) == 0 {
			t.Fatal("empty sequence")
		}
		t.Logf("seq=%d params P=%d K=%d C=%d G=%d N=%d steps=%d final cur=%d low=%d",
			seq, p, k, c, g, n, len(ops), pl1.Cur(), pl1.Low())
		for _, line := range log {
			t.Log(line)
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}
