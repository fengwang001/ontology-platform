package netcode

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This file cross-checks the real implementation against an independent,
// deliberately naive model over randomized message interleavings, and
// checks deterministic replay of the server.

// mStep is the model's own copy of the step rule, written independently of
// applyStep so a bug in one cannot hide in the other.
func mStep(pos, delta, w, m int64) (int64, bool) {
	abs := delta
	if abs < 0 {
		abs = -abs
	}
	if abs > m {
		return pos, false
	}
	p := pos + delta
	if p < 0 {
		p = 0
	}
	if p > w {
		p = w
	}
	return p, true
}

// --- naive server: scans every player every tick, keeps full queues ------

type mPlayer struct {
	pos     int64
	maxRecv int64
	proc    int64
	pend    []Move
}

type mServer struct {
	cfg     Config
	players map[string]*mPlayer
	order   []string
	ticked  bool
	last    int64
}

func newMServer(cfg Config) *mServer {
	return &mServer{cfg: cfg, players: map[string]*mPlayer{}}
}

func (m *mServer) register(id string) {
	m.players[id] = &mPlayer{}
	m.order = append(m.order, id)
}

func (m *mServer) receive(id string, mv Move) (Receipt, error) {
	if mv.Delta == 0 {
		return Receipt{}, &Error{Reason: ReasonInvalidDelta, Player: id, Seq: mv.Seq}
	}
	if mv.Seq < 1 {
		return Receipt{}, &Error{Reason: ReasonInvalidSeq, Player: id, Seq: mv.Seq}
	}
	p, ok := m.players[id]
	if !ok {
		return Receipt{}, &Error{Reason: ReasonUnknownPlayer, Player: id, Seq: mv.Seq}
	}
	if mv.Seq <= p.maxRecv {
		return Receipt{Status: StatusDuplicate, Previous: StatusAccepted}, nil
	}
	if mv.Seq > p.maxRecv+1 {
		return Receipt{Status: StatusGap}, nil
	}
	if len(p.pend) >= m.cfg.P {
		return Receipt{Status: StatusBacklogFull}, nil
	}
	p.pend = append(p.pend, mv)
	p.maxRecv = mv.Seq
	return Receipt{Status: StatusAccepted}, nil
}

func (m *mServer) tick(now int64) ([]Ack, error) {
	if m.ticked && now <= m.last {
		return nil, &Error{Reason: ReasonClockRollback, Now: now, LastTick: m.last}
	}
	m.ticked = true
	m.last = now
	var acks []Ack
	for _, id := range m.order {
		p := m.players[id]
		if len(p.pend) == 0 {
			continue
		}
		n := len(p.pend)
		if n > m.cfg.K {
			n = m.cfg.K
		}
		var rej []int64
		for i := 0; i < n; i++ {
			mv := p.pend[i]
			pos, ok := mStep(p.pos, mv.Delta, m.cfg.W, m.cfg.M)
			p.pos = pos
			if !ok {
				rej = append(rej, mv.Seq)
			}
			p.proc = mv.Seq
		}
		p.pend = append([]Move(nil), p.pend[n:]...)
		acks = append(acks, Ack{Player: id, ProcessedSeq: p.proc, Position: p.pos, Rejected: rej})
	}
	return acks, nil
}

// --- naive client: keeps the full history and recomputes from scratch ---

type mClient struct {
	cfg   Config
	hist  []Move
	acked int64
	base  int64
	rej   map[int64]bool
}

func newMClient(cfg Config) *mClient {
	return &mClient{cfg: cfg, rej: map[int64]bool{}}
}

func (mc *mClient) submit(delta int64) Move {
	mv := Move{Seq: int64(len(mc.hist)) + 1, Delta: delta}
	mc.hist = append(mc.hist, mv)
	return mv
}

func (mc *mClient) applyAck(a Ack) bool {
	if a.ProcessedSeq <= mc.acked {
		return false
	}
	mc.acked = a.ProcessedSeq
	mc.base = a.Position
	for _, s := range a.Rejected {
		mc.rej[s] = true
	}
	return true
}

func (mc *mClient) predicted() int64 {
	pos := mc.base
	for _, mv := range mc.hist {
		if mv.Seq <= mc.acked || mc.rej[mv.Seq] {
			continue
		}
		pos, _ = mStep(pos, mv.Delta, mc.cfg.W, mc.cfg.M)
	}
	return pos
}

// --- randomized interleaving driver ---------------------------------------

func sortedAcks(acks []Ack) []Ack {
	out := append([]Ack(nil), acks...)
	sort.Slice(out, func(i, j int) bool { return out[i].Player < out[j].Player })
	return out
}

func reasonOf(err error) string {
	if err == nil {
		return "<nil>"
	}
	if ne, ok := err.(*Error); ok {
		return ne.Reason.String()
	}
	return err.Error()
}

// TestRandomInterleavingsAgainstModel drives the real server/client and
// the naive model through identical random interleavings of submits,
// duplicate resends, gap attempts, invalid inputs, ticks and (reordered,
// duplicated) ack deliveries, then compares every observable output.
func TestRandomInterleavingsAgainstModel(t *testing.T) {
	const cases = 1500
	for tc := 0; tc < cases; tc++ {
		rng := rand.New(rand.NewSource(int64(tc)*7919 + 42))
		cfg := Config{
			W: 1 + int64(rng.Intn(30)),
			K: 1 + rng.Intn(3),
			M: 1 + int64(rng.Intn(8)),
			P: 1 + rng.Intn(4),
		}
		ids := []string{"p0", "p1", "p2"}[:1+rng.Intn(3)]

		srv, err := NewServer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		msv := newMServer(cfg)
		cls := map[string]*Client{}
		mcs := map[string]*mClient{}
		nextSeq := map[string]int64{}
		submitted := map[string][]Move{}
		recv := map[string]int64{}
		for _, id := range ids {
			if err := srv.Register(id); err != nil {
				t.Fatal(err)
			}
			msv.register(id)
			c, err := NewClient(cfg, id)
			if err != nil {
				t.Fatal(err)
			}
			cls[id] = c
			mcs[id] = newMClient(cfg)
			nextSeq[id] = 1
		}

		var (
			now    int64
			buf    []Ack
			trace  []string
			counts = map[string]int{}
		)
		fail := func(format string, args ...any) {
			t.Helper()
			t.Errorf("case %d cfg=%+v: %s", tc, cfg, fmt.Sprintf(format, args...))
			for i, line := range trace {
				t.Logf("  trace[%d]: %s", i, line)
			}
			t.FailNow()
		}
		nonzeroDelta := func() int64 {
			d := int64(rng.Intn(2*int(cfg.M)+7)) - cfg.M - 3
			if d == 0 {
				d = 1
			}
			return d
		}
		checkReceive := func(id string, mv Move) Receipt {
			r1, e1 := srv.Receive(id, mv)
			r2, e2 := msv.receive(id, mv)
			if r1 != r2 || reasonOf(e1) != reasonOf(e2) {
				fail("receive %s %+v: real=(%+v,%v) model=(%+v,%v)",
					id, mv, r1, e1, r2, e2)
			}
			return r1
		}
		checkPredicted := func(id string) {
			got, _ := cls[id].Predicted()
			if want := mcs[id].predicted(); got != want {
				fail("predicted %s: real=%d model=%d", id, got, want)
			}
		}
		// noteReceipt keeps recv[id] equal to the server's max received seq
		// (the contiguous accepted prefix). An Accepted receipt always
		// carries seq == recv[id]+1; a Duplicate for recv[id]+1 means a
		// "duplicate resend" event actually delivered it earlier.
		noteReceipt := func(id string, mv Move, r Receipt) {
			if (r.Status == StatusAccepted || r.Status == StatusDuplicate) && mv.Seq == recv[id]+1 {
				recv[id] = mv.Seq
			}
		}
		doTick := func() {
			now += 1 + int64(rng.Intn(3))
			a1, e1 := srv.Tick(now)
			a2, e2 := msv.tick(now)
			if e1 != nil || e2 != nil || !reflect.DeepEqual(sortedAcks(a1), sortedAcks(a2)) {
				fail("tick now=%d: real=(%+v,%v) model=(%+v,%v)", now, a1, e1, a2, e2)
			}
			buf = append(buf, a1...)
			counts["ticks"]++
			counts["acks"] += len(a1)
		}
		deliver := func(all bool) {
			rng.Shuffle(len(buf), func(i, j int) { buf[i], buf[j] = buf[j], buf[i] })
			n := len(buf)
			if !all {
				n = rng.Intn(n + 1)
			}
			for _, ack := range buf[:n] {
				times := 1
				if rng.Intn(3) == 0 {
					times = 2 // duplicate delivery
				}
				for ; times > 0; times-- {
					ok1 := cls[ack.Player].ApplyAck(ack)
					ok2 := mcs[ack.Player].applyAck(ack)
					if ok1 != ok2 {
						fail("applyAck %+v: real=%v model=%v", ack, ok1, ok2)
					}
					checkPredicted(ack.Player)
				}
				counts["delivered"]++
			}
			buf = buf[n:]
		}

		nEvents := 30 + rng.Intn(50)
		for e := 0; e < nEvents; e++ {
			pick := rng.Intn(100)
			id := ids[rng.Intn(len(ids))]
			switch {
			case pick < 45: // submit + receive
				d := nonzeroDelta()
				mv, err := cls[id].Submit(d)
				if err != nil {
					fail("submit delta=%d: %v", d, err)
				}
				mmv := mcs[id].submit(d)
				if mv != mmv {
					fail("submit seq: real=%+v model=%+v", mv, mmv)
				}
				noteReceipt(id, mv, checkReceive(id, mv))
				submitted[id] = append(submitted[id], mv)
				checkPredicted(id)
				nextSeq[id]++
				counts["submits"]++
				trace = append(trace, fmt.Sprintf("submit %s %+v", id, mv))
			case pick < 58 && nextSeq[id] > 1: // duplicate resend
				mv := Move{Seq: 1 + rng.Int63n(nextSeq[id]-1), Delta: nonzeroDelta()}
				noteReceipt(id, mv, checkReceive(id, mv))
				counts["dups"]++
				trace = append(trace, fmt.Sprintf("dup %s %+v", id, mv))
			case pick < 70: // gap attempt
				mv := Move{Seq: nextSeq[id] + 1 + int64(rng.Intn(2)), Delta: nonzeroDelta()}
				checkReceive(id, mv)
				counts["gaps"]++
				trace = append(trace, fmt.Sprintf("gap %s %+v", id, mv))
			case pick < 78: // invalid input
				// Always a hard error: either delta == 0 with a valid
				// seq, or seq < 1 with a non-zero delta. Never inject a
				// valid move behind the client's back, which would
				// desync the client-owned seq space.
				mv := Move{Seq: 1 + int64(rng.Intn(5)), Delta: 0}
				if rng.Intn(2) == 0 {
					mv = Move{Seq: -int64(rng.Intn(3)), Delta: nonzeroDelta()}
				}
				checkReceive(id, mv)
				counts["invalids"]++
				trace = append(trace, fmt.Sprintf("invalid %s %+v", id, mv))
			case pick < 90: // tick
				doTick()
				trace = append(trace, fmt.Sprintf("tick now=%d", now))
			default: // deliver some buffered acks
				deliver(false)
				trace = append(trace, "deliver")
			}
		}

		// Drain: resend inputs the server rejected (backlog full / gap
		// cascading from it), process everything, deliver every ack
		// (shuffled, with duplicates), then final consistency must hold.
		for {
			done := true
			for _, id := range ids {
				for recv[id] < int64(len(submitted[id])) {
					mv := submitted[id][recv[id]]
					r := checkReceive(id, mv)
					if r.Status != StatusAccepted && r.Status != StatusDuplicate {
						break
					}
					noteReceipt(id, mv, r)
					counts["resends"]++
				}
				if recv[id] < int64(len(submitted[id])) {
					done = false
				}
				if n, _ := srv.PendingLen(id); n > 0 {
					done = false
				}
			}
			if done {
				break
			}
			doTick()
		}
		deliver(true)
		if len(buf) != 0 {
			fail("undelivered acks remain: %d", len(buf))
		}
		final := ""
		for _, id := range ids {
			spos, sseq, _ := srv.Authoritative(id)
			cpos, unacked := cls[id].Predicted()
			if cpos != spos || len(unacked) != 0 || cls[id].Divergence() != 0 || cls[id].LastAcked() != sseq {
				fail("final %s: client=(pos=%d unacked=%v div=%d acked=%d) server=(pos=%d seq=%d)",
					id, cpos, unacked, cls[id].Divergence(), cls[id].LastAcked(), spos, sseq)
			}
			if mp := mcs[id].predicted(); mp != spos {
				fail("final %s: model predicted=%d server=%d", id, mp, spos)
			}
			final += fmt.Sprintf(" %s=pos%d/seq%d", id, spos, sseq)
		}
		t.Logf("case %d OK: cfg=%+v players=%d events=%d counts=%v final[%s] "+
			"判定依据: 所有输入已处理且全部确认已送达 => 预测==权威, 未确认为空, 偏差为0, 已确认序号一致",
			tc, cfg, len(ids), nEvents, counts, final)
	}
}

// TestServerDeterministicReplay replays one fixed serial script on two
// fresh servers and requires byte-identical receipts and ack sequences.
func TestServerDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	cfg := Config{W: 25, K: 2, M: 6, P: 3}
	ids := []string{"a", "b"}

	type op struct {
		isTick bool
		player string
		mv     Move
		now    int64
	}
	var script []op
	next := map[string]int64{"a": 1, "b": 1}
	var now int64
	for i := 0; i < 300; i++ {
		id := ids[rng.Intn(2)]
		switch rng.Intn(5) {
		case 0:
			now += 1 + int64(rng.Intn(3))
			script = append(script, op{isTick: true, now: now})
		case 1:
			script = append(script, op{player: id, mv: Move{Seq: next[id] + 2, Delta: 1}}) // gap
		case 2:
			if next[id] > 1 {
				script = append(script, op{player: id, mv: Move{Seq: 1 + rng.Int63n(next[id]-1), Delta: 1}}) // dup
			}
		default:
			d := int64(rng.Intn(17)) - 8
			if d == 0 {
				d = 1
			}
			script = append(script, op{player: id, mv: Move{Seq: next[id], Delta: d}})
			next[id]++
		}
	}

	run := func() ([]Receipt, [][]Ack) {
		s, err := NewServer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := s.Register(id); err != nil {
				t.Fatal(err)
			}
		}
		var receipts []Receipt
		var acks [][]Ack
		for _, o := range script {
			if o.isTick {
				a, err := s.Tick(o.now)
				if err != nil {
					t.Fatal(err)
				}
				acks = append(acks, a)
			} else {
				r, _ := s.Receive(o.player, o.mv)
				receipts = append(receipts, r)
			}
		}
		return receipts, acks
	}
	r1, a1 := run()
	r2, a2 := run()
	if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(a1, a2) {
		t.Fatalf("non-deterministic replay:\nreceipts %v vs %v\nacks %v vs %v", r1, r2, a1, a2)
	}
	t.Logf("deterministic replay verified over %d ops, %d ticks", len(script), len(a1))
}
