package bbr

import (
	"math/rand/v2"
	"strings"
	"testing"
)

type naiveSample struct {
	round int64
	value int64
}

type naiveModel struct {
	mss     int64
	d       int64
	round   int64
	next    int64
	samples []naiveSample
	minRtt  int64
	stamp   int64
	state   State
	filled  bool
	fullBw  int64
	fullCnt int
	ci      int
	cs      int64
	pd      int64
	lastNow int64
}

type naiveDecision struct {
	sample    int64
	newRound  bool
	expired   bool
	bdp       int64
	before    State
	inserted  bool
	advanced  bool
	gain      int
	probeExit bool
}

func (m *naiveModel) maxBw() int64 {
	max := int64(0)
	for _, sample := range m.samples {
		if sample.value > max {
			max = sample.value
		}
	}
	return max
}

func (m *naiveModel) onAck(now int64, n int64, rtt int64, ds int64, inflight int64, appLimited bool) naiveDecision {
	decision := naiveDecision{before: m.state}
	m.d += n
	if ds >= m.next {
		m.round++
		m.next = m.d
		decision.newRound = true
	}
	decision.sample = (m.d - ds) * 1000 / rtt

	kept := m.samples[:0]
	for _, sample := range m.samples {
		if m.round-sample.round < 10 {
			kept = append(kept, sample)
		}
	}
	m.samples = kept

	currentMax := m.maxBw()
	if !appLimited || decision.sample >= currentMax {
		m.samples = append(m.samples, naiveSample{round: m.round, value: decision.sample})
		decision.inserted = true
	}
	maxBw := m.maxBw()

	decision.expired = m.minRtt != 0 && now-m.stamp >= 10_000
	if m.minRtt == 0 || rtt <= m.minRtt || decision.expired {
		m.minRtt = rtt
		m.stamp = now
	}

	if decision.newRound && !appLimited && !m.filled {
		if maxBw*100 >= m.fullBw*125 {
			m.fullBw = maxBw
			m.fullCnt = 0
		} else {
			m.fullCnt++
			if m.fullCnt == 3 {
				m.filled = true
			}
		}
		if m.state == Startup && m.filled {
			m.state = Drain
		}
	}

	decision.bdp = mulDivFloor(maxBw, m.minRtt, 1000)
	if m.state == Drain && inflight <= decision.bdp {
		m.state = ProbeBW
		m.ci = 2
		m.cs = now
	}

	if decision.before == ProbeBW {
		gains := [...]int{125, 75, 100, 100, 100, 100, 100, 100}
		decision.gain = gains[m.ci]
		elapsed := now - m.cs
		advance := false
		switch decision.gain {
		case 100:
			advance = elapsed >= m.minRtt
		case 125:
			advance = elapsed >= m.minRtt && inflight >= mulDivFloor(decision.bdp, 125, 100)
		case 75:
			advance = elapsed >= m.minRtt || inflight <= decision.bdp
		}
		if advance {
			m.ci = (m.ci + 1) % 8
			m.cs = now
			decision.advanced = true
		}
	}

	if decision.expired && m.state != ProbeRTT {
		m.state = ProbeRTT
		m.pd = 0
	}
	if m.state == ProbeRTT {
		if m.pd == 0 && inflight <= 4*m.mss {
			m.pd = now + 200
		}
		if m.pd != 0 && now >= m.pd {
			m.stamp = now
			m.pd = 0
			if m.filled {
				m.state = ProbeBW
				m.ci = 2
				m.cs = now
			} else {
				m.state = Startup
			}
			decision.probeExit = true
		}
	}

	m.lastNow = now
	return decision
}

func (m *naiveModel) pacing() int64 {
	if m.d == 0 {
		return 0
	}
	pg := int64(100)
	switch m.state {
	case Startup:
		pg = 289
	case Drain:
		pg = 35
	case ProbeBW:
		pg = int64(probeGain(m.ci))
	case ProbeRTT:
		pg = 100
	}
	return m.maxBw() * pg / 100
}

func (m *naiveModel) cwnd() int64 {
	if m.d == 0 {
		return 10 * m.mss
	}
	if m.state == ProbeRTT {
		return 4 * m.mss
	}
	cg := int64(289)
	if m.state == ProbeBW {
		cg = 200
	}
	window := mulDivFloor(mulDivFloor(m.maxBw(), m.minRtt, 1000), cg, 100)
	minimum := 4 * m.mss
	if window < minimum {
		return minimum
	}
	return window
}

func TestRandomSequencesMatchNaiveModel(t *testing.T) {
	const sequences = 2000
	for sequence := 0; sequence < sequences; sequence++ {
		rng := rand.New(rand.NewPCG(uint64(sequence), 99))
		mss := int64(500 + rng.IntN(2001))
		actual, err := New(mss)
		if err != nil {
			t.Fatalf("New(%d): %v", mss, err)
		}
		model := &naiveModel{mss: mss}
		length := 15 + rng.IntN(25)
		var log strings.Builder
		log.WriteString("\nsequence inputs/outputs/decisions:\n")

		now := int64(0)
		for ack := 0; ack < length; ack++ {
			var ds int64
			if model.next == 0 || rng.IntN(4) != 0 {
				ds = model.next
			} else {
				ds = rng.Int64N(model.next)
			}
			n := int64(1 + rng.IntN(5000))
			rtt := int64(1 + rng.IntN(200))
			inflight := int64(rng.IntN(20001))
			if rng.IntN(6) == 0 {
				inflight = rng.Int64N(4*mss + 1)
			}
			appLimited := rng.IntN(6) == 0
			now += int64(rng.IntN(700))
			if rng.IntN(10) == 0 {
				now += int64(9000 + rng.IntN(3001))
			}

			log.WriteString("input")
			writeAckFields(&log, now, n, rtt, ds, inflight, appLimited)
			before := actual.State()
			err := actual.OnAck(now, n, rtt, ds, inflight, appLimited)
			if err != nil {
				t.Fatalf("sequence=%d ack=%d unexpected error %v; %s", sequence, ack, err, log.String())
			}
			decision := model.onAck(now, n, rtt, ds, inflight, appLimited)

			writeDecisionFields(&log, decision, before, actual.State())
			writeOutputFields(&log, actual.Pacing(), actual.Cwnd(), actual.MaxBw(), actual.MinRtt(), actual.Round())
			t.Log(log.String())

			compareNaiveModel(t, sequence, ack, actual, model, log.String())
			log.Reset()
		}
		if actual.FilterOps() > int64(3*length) {
			t.Fatalf("sequence=%d filterOps=%d exceeds 3*%d; %s", sequence, actual.FilterOps(), length, log.String())
		}
	}
}

func compareNaiveModel(t *testing.T, sequence int, ack int, actual *Controller, model *naiveModel, log string) {
	t.Helper()
	pairs := []struct {
		name        string
		got, wanted int64
	}{
		{"D", actual.d, model.d},
		{"R", actual.r, model.round},
		{"N", actual.n, model.next},
		{"maxBw", actual.maxBwLocked(), model.maxBw()},
		{"minRtt", actual.minRtt, model.minRtt},
		{"stamp", actual.stamp, model.stamp},
		{"fullBw", actual.fullBw, model.fullBw},
		{"ci", int64(actual.ci), int64(model.ci)},
		{"cs", actual.cs, model.cs},
		{"pd", actual.pd, model.pd},
		{"Pacing", actual.Pacing(), model.pacing()},
		{"Cwnd", actual.Cwnd(), model.cwnd()},
	}
	for _, pair := range pairs {
		if pair.got != pair.wanted {
			t.Fatalf("sequence=%d ack=%d %s=%d want %d; %s", sequence, ack, pair.name, pair.got, pair.wanted, log)
		}
	}
	if actual.state != model.state || actual.filled != model.filled || actual.fullCnt != model.fullCnt {
		t.Fatalf("sequence=%d ack=%d state=(%s,%t,%d) want (%s,%t,%d); %s",
			sequence, ack, actual.state, actual.filled, actual.fullCnt,
			model.state, model.filled, model.fullCnt, log)
	}
}

func writeAckFields(log *strings.Builder, now int64, n int64, rtt int64, ds int64, inflight int64, appLimited bool) {
	log.WriteString(" now=")
	log.WriteString(itoa(now))
	log.WriteString(" n=")
	log.WriteString(itoa(n))
	log.WriteString(" rtt=")
	log.WriteString(itoa(rtt))
	log.WriteString(" ds=")
	log.WriteString(itoa(ds))
	log.WriteString(" inflight=")
	log.WriteString(itoa(inflight))
	if appLimited {
		log.WriteString(" appLimited=true")
	}
}

func writeDecisionFields(log *strings.Builder, decision naiveDecision, before State, after State) {
	log.WriteString(" basis sample=")
	log.WriteString(itoa(decision.sample))
	log.WriteString(" newRound=")
	log.WriteString(boolString(decision.newRound))
	log.WriteString(" expired=")
	log.WriteString(boolString(decision.expired))
	log.WriteString(" bdp=")
	log.WriteString(itoa(decision.bdp))
	log.WriteString(" inserted=")
	log.WriteString(boolString(decision.inserted))
	log.WriteString(" state=")
	log.WriteString(before.String())
	log.WriteString("->")
	log.WriteString(after.String())
	if decision.before == ProbeBW {
		log.WriteString(" gain=")
		log.WriteString(itoa(int64(decision.gain)))
		log.WriteString(" advanced=")
		log.WriteString(boolString(decision.advanced))
	}
	if decision.probeExit {
		log.WriteString(" probeRTTExit=true")
	}
}

func writeOutputFields(log *strings.Builder, pacing int64, cwnd int64, maxBw int64, minRtt int64, round int64) {
	log.WriteString(" output pacing=")
	log.WriteString(itoa(pacing))
	log.WriteString(" cwnd=")
	log.WriteString(itoa(cwnd))
	log.WriteString(" maxBw=")
	log.WriteString(itoa(maxBw))
	log.WriteString(" minRtt=")
	log.WriteString(itoa(minRtt))
	log.WriteString(" round=")
	log.WriteString(itoa(round))
	log.WriteByte('\n')
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits [20]byte
	pos := len(digits)
	for v > 0 {
		pos--
		digits[pos] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}
	return string(digits[pos:])
}
