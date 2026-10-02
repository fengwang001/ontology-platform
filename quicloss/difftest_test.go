package quicloss

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

var diffTrace = os.Getenv("QUICLOSS_DIFF_TRACE") == "1"

// naivePkt is the independent reference model's packet record.
type naivePkt struct {
	pn, sentAt int64
	size       int
	ae         bool
}

type naiveSpace struct {
	pkts      map[int64]naivePkt
	maxSent   int64
	hasSent   bool
	la        int64
	hasLA     bool
	lt        int64
	hasLT     bool
	discarded bool
}

// naiveModel re-implements every rule step by step, deliberately using
// different container/iteration choices than the production code.
type naiveModel struct {
	maxAckDelay int64
	H, A        naiveSpace
	latest      int64
	srtt        int64
	rttvar      int64
	minRtt      int64
	hasSample   bool
	ptoCount    int
	confirmed   bool
	lastNow     int64
	hasClock    bool
}

func newNaive(maxAckDelay int64) *naiveModel {
	m := &naiveModel{
		maxAckDelay: maxAckDelay,
		latest:      333,
		srtt:        333,
		rttvar:      166,
	}
	m.H = naiveSpace{pkts: map[int64]naivePkt{}}
	m.A = naiveSpace{pkts: map[int64]naivePkt{}}
	return m
}

func (m *naiveModel) spOf(id byte) *naiveSpace {
	if id == SpaceH {
		return &m.H
	}
	return &m.A
}

func validSpace(id byte) bool { return id == SpaceH || id == SpaceA }

func (m *naiveModel) checkTime(now int64) error {
	if now < 0 || now > MaxTime {
		return ErrInvalidTime
	}
	return nil
}

func (m *naiveModel) Send(now int64, id byte, pn int64, size int, ae bool) error {
	if !validSpace(id) {
		return ErrInvalidSpace
	}
	if size < 1 || size > 65535 {
		return ErrInvalidSize
	}
	if err := m.checkTime(now); err != nil {
		return err
	}
	if pn < 0 {
		return ErrNegativePN
	}
	if m.hasClock && now < m.lastNow {
		return ErrClockBackwards
	}
	if id == SpaceH && m.confirmed {
		return ErrSpaceDiscarded
	}
	sp := m.spOf(id)
	if sp.hasSent && pn <= sp.maxSent {
		return ErrPNNotIncreasing
	}
	sp.pkts[pn] = naivePkt{pn: pn, sentAt: now, size: size, ae: ae}
	sp.maxSent, sp.hasSent = pn, true
	m.lastNow, m.hasClock = now, true
	return nil
}

func (m *naiveModel) detect(id byte, now int64) []int64 {
	sp := m.spOf(id)
	sp.hasLT = false
	sp.lt = 0
	lost := []int64{}
	if !sp.hasLA {
		return lost
	}
	base := m.latest
	if m.srtt > base {
		base = m.srtt
	}
	delay := 9 * base / 8
	if delay < 1 {
		delay = 1
	}
	for pn, p := range sp.pkts {
		if pn >= sp.la {
			continue
		}
		if sp.la-pn >= 3 || p.sentAt <= now-delay {
			lost = append(lost, pn)
			delete(sp.pkts, pn)
			continue
		}
		cand := p.sentAt + delay
		if !sp.hasLT || cand < sp.lt {
			sp.lt, sp.hasLT = cand, true
		}
	}
	sort.Slice(lost, func(i, j int) bool { return lost[i] < lost[j] })
	return lost
}

func (m *naiveModel) Ack(now int64, id byte, pns []int64, ackDelay int64) ([]int64, error) {
	if !validSpace(id) {
		return nil, ErrInvalidSpace
	}
	if err := m.checkTime(now); err != nil {
		return nil, err
	}
	if len(pns) == 0 {
		return nil, ErrEmptyAck
	}
	for _, pn := range pns {
		if pn < 0 {
			return nil, ErrNegativePN
		}
	}
	if m.hasClock && now < m.lastNow {
		return nil, ErrClockBackwards
	}
	if id == SpaceH && m.confirmed {
		return nil, ErrSpaceDiscarded
	}
	sp := m.spOf(id)
	for _, pn := range pns {
		if !sp.hasSent || pn > sp.maxSent {
			return nil, ErrPNNeverSent
		}
	}
	m.lastNow, m.hasClock = now, true

	newly := map[int64]naivePkt{}
	for _, pn := range pns {
		if p, ok := sp.pkts[pn]; ok {
			newly[pn] = p
		}
	}
	if len(newly) == 0 {
		return []int64{}, nil
	}
	var largest int64 = -1
	for pn := range newly {
		if pn > largest {
			largest = pn
		}
	}
	if !sp.hasLA || largest > sp.la {
		sp.la, sp.hasLA = largest, true
	}
	lp := newly[largest]
	if lp.ae {
		sample := now - lp.sentAt
		if sample < 1 {
			sample = 1
		}
		m.latest = sample
		if !m.hasSample {
			m.minRtt, m.srtt, m.rttvar, m.hasSample = sample, sample, sample/2, true
		} else {
			if sample < m.minRtt {
				m.minRtt = sample
			}
			var ad int64
			if id == SpaceA && m.confirmed {
				ad = ackDelay
				if ad > m.maxAckDelay {
					ad = m.maxAckDelay
				}
				if ad < 0 {
					ad = 0
				}
			}
			adj := sample
			if sample >= m.minRtt+ad {
				adj = sample - ad
			}
			d := m.srtt - adj
			if d < 0 {
				d = -d
			}
			m.rttvar = (3*m.rttvar + d) / 4
			m.srtt = (7*m.srtt + adj) / 8
		}
	}
	clearPTO := false
	for pn, p := range newly {
		if p.ae {
			clearPTO = true
		}
		delete(sp.pkts, pn)
	}
	if clearPTO {
		m.ptoCount = 0
	}
	return m.detect(id, now), nil
}

func (m *naiveModel) ptoPeriod(id byte) int64 {
	v := 4 * m.rttvar
	if v < 1 {
		v = 1
	}
	p := m.srtt + v
	if id == SpaceA {
		p += m.maxAckDelay
	}
	return p
}

func (m *naiveModel) timer() (TimerInfo, bool) {
	var best TimerInfo
	have := false
	considerLoss := func(id byte) {
		sp := m.spOf(id)
		if sp.hasLT && (!have || sp.lt < best.Time) {
			best, have = TimerInfo{Time: sp.lt, Space: id, Mode: ModeLoss}, true
		}
	}
	considerLoss(SpaceH)
	considerLoss(SpaceA)
	if have {
		return best, true
	}
	considerPTO := func(id byte) {
		sp := m.spOf(id)
		var last int64
		found := false
		for _, p := range sp.pkts {
			if p.ae && (!found || p.sentAt > last) {
				last, found = p.sentAt, true
			}
		}
		if !found {
			return
		}
		shift := m.ptoCount
		if shift > 20 {
			shift = 20
		}
		t := last + m.ptoPeriod(id)*(int64(1)<<uint(shift))
		if !have || t < best.Time {
			best, have = TimerInfo{Time: t, Space: id, Mode: ModePTO}, true
		}
	}
	considerPTO(SpaceH)
	if m.confirmed {
		considerPTO(SpaceA)
	}
	return best, have
}

type naiveResult struct {
	lost  []int64
	probe *ProbeRequest
}

func (m *naiveModel) OnTimeout(now int64) (naiveResult, error) {
	if err := m.checkTime(now); err != nil {
		return naiveResult{}, err
	}
	if m.hasClock && now < m.lastNow {
		return naiveResult{}, ErrClockBackwards
	}
	info, ok := m.timer()
	if !ok {
		return naiveResult{}, ErrNoTimer
	}
	if now < info.Time {
		return naiveResult{}, ErrTimeoutEarly
	}
	m.lastNow, m.hasClock = now, true
	if info.Mode == ModeLoss {
		return naiveResult{lost: m.detect(info.Space, now)}, nil
	}
	m.ptoCount++
	return naiveResult{probe: &ProbeRequest{Space: info.Space}}, nil
}

func (m *naiveModel) Confirm(now int64) error {
	if err := m.checkTime(now); err != nil {
		return err
	}
	if m.hasClock && now < m.lastNow {
		return ErrClockBackwards
	}
	m.lastNow, m.hasClock = now, true
	m.confirmed, m.ptoCount = true, 0
	m.H = naiveSpace{pkts: map[int64]naivePkt{}}
	return nil
}

func (m *naiveModel) Detect(id byte, now int64) ([]int64, error) {
	if !validSpace(id) {
		return nil, ErrInvalidSpace
	}
	if err := m.checkTime(now); err != nil {
		return nil, err
	}
	if m.hasClock && now < m.lastNow {
		return nil, ErrClockBackwards
	}
	if id == SpaceH && m.confirmed {
		return nil, ErrSpaceDiscarded
	}
	m.lastNow, m.hasClock = now, true
	return m.detect(id, now), nil
}

type fullState struct {
	pkts                         map[string]map[int64]naivePkt
	la, lt                       map[string]int64
	hasLA, hasLT, discarded      map[string]bool
	latest, srtt, rttvar, minRtt int64
	hasSample, confirmed         bool
	ptoCount                     int
	lastNow                      int64
	hasClock                     bool
}

func captureNaive(m *naiveModel) fullState {
	fs := fullState{
		pkts:      map[string]map[int64]naivePkt{},
		la:        map[string]int64{},
		lt:        map[string]int64{},
		hasLA:     map[string]bool{},
		hasLT:     map[string]bool{},
		discarded: map[string]bool{},
		latest:    m.latest,
		srtt:      m.srtt,
		rttvar:    m.rttvar,
		minRtt:    m.minRtt,
		hasSample: m.hasSample,
		confirmed: m.confirmed,
		ptoCount:  m.ptoCount,
		lastNow:   m.lastNow,
		hasClock:  m.hasClock,
	}
	for name, sp := range map[string]*naiveSpace{"H": &m.H, "A": &m.A} {
		cp := make(map[int64]naivePkt, len(sp.pkts))
		for pn, p := range sp.pkts {
			cp[pn] = p
		}
		fs.pkts[name] = cp
		fs.la[name], fs.hasLA[name] = sp.la, sp.hasLA
		fs.lt[name], fs.hasLT[name] = sp.lt, sp.hasLT
		fs.discarded[name] = name == "H" && m.confirmed
	}
	return fs
}

func captureReal(c *Controller) fullState {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs := fullState{
		pkts:      map[string]map[int64]naivePkt{},
		la:        map[string]int64{},
		lt:        map[string]int64{},
		hasLA:     map[string]bool{},
		hasLT:     map[string]bool{},
		discarded: map[string]bool{},
		latest:    c.latest,
		srtt:      c.srtt,
		rttvar:    c.rttvar,
		minRtt:    c.minRTT,
		hasSample: c.hasSample,
		confirmed: c.confirmed,
		ptoCount:  c.ptoCount,
		lastNow:   c.lastNow,
		hasClock:  c.hasClock,
	}
	grab := func(name string, sp *space) {
		cp := make(map[int64]naivePkt, len(sp.packets))
		for pn, p := range sp.packets {
			cp[pn] = naivePkt{pn: pn, sentAt: p.sentAt, size: p.size, ae: p.ae}
		}
		fs.pkts[name] = cp
		fs.la[name], fs.hasLA[name] = sp.largestAck, sp.hasLA
		fs.lt[name], fs.hasLT[name] = sp.lossTime, sp.hasLT
		fs.discarded[name] = name == "H" && c.confirmed
	}
	grab("H", &c.h)
	grab("A", &c.a)
	return fs
}

func statesEqual(a, b fullState) (string, bool) {
	scalar := func(fs fullState) string {
		return fmt.Sprintf("latest=%d srtt=%d rttvar=%d min=%d sample=%v confirmed=%v pto=%d clock=(%v,%d)",
			fs.latest, fs.srtt, fs.rttvar, fs.minRtt, fs.hasSample, fs.confirmed, fs.ptoCount, fs.hasClock, fs.lastNow)
	}
	if scalar(a) != scalar(b) {
		return "globals: " + scalar(a) + " vs " + scalar(b), false
	}
	for _, name := range []string{"H", "A"} {
		if len(a.pkts[name]) != len(b.pkts[name]) {
			return fmt.Sprintf("%s packet count %d vs %d", name, len(a.pkts[name]), len(b.pkts[name])), false
		}
		for pn, pa := range a.pkts[name] {
			pb, ok := b.pkts[name][pn]
			if !ok || pa != pb {
				return fmt.Sprintf("%s packet %d: %+v vs %+v", name, pn, pa, pb), false
			}
		}
		if a.la[name] != b.la[name] || a.hasLA[name] != b.hasLA[name] ||
			a.lt[name] != b.lt[name] || a.hasLT[name] != b.hasLT[name] ||
			a.discarded[name] != b.discarded[name] {
			return fmt.Sprintf("%s edges: la=(%d,%v) lt=(%d,%v) disc=%v vs la=(%d,%v) lt=(%d,%v) disc=%v",
				name, a.la[name], a.hasLA[name], a.lt[name], a.hasLT[name], a.discarded[name],
				b.la[name], b.hasLA[name], b.lt[name], b.hasLT[name], b.discarded[name]), false
		}
	}
	return "", true
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

// TestRandomDifferential replays 2000 random call sequences against both the
// production controller and an independently written naive model, logging
// every input, output and decision basis, then comparing the complete state.
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 60
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		maxAD := []int64{0, 10, 25, 100, 10000}[rng.Intn(5)]
		real, err := New(maxAD)
		if err != nil {
			t.Fatal(err)
		}
		ref := newNaive(maxAD)
		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d maxAckDelay=%d\n", seed, maxAD)

		var now int64
		nextPN := map[byte]int64{SpaceH: 0, SpaceA: 0}
		sentAny := map[byte]bool{}

		flush := func(op int) {
			if reason, ok := statesEqual(captureReal(real), captureNaive(ref)); !ok {
				t.Fatalf("seed=%d state mismatch after op %d: %s\n%s", seed, op, reason, log.String())
			}
			if diffTrace {
				t.Logf("%s", strings.TrimRight(log.String(), "\n"))
			}
			log.Reset()
			fmt.Fprintf(&log, "seed=%d maxAckDelay=%d (cont)\n", seed, maxAD)
		}

		for op := 0; op < opsPerSeq; op++ {
			kind := rng.Intn(10)
			id := []byte{SpaceH, SpaceA}[rng.Intn(2)]

			now += rng.Int63n(120)
			if rng.Intn(6) == 0 {
				now += rng.Int63n(900)
			}

			switch kind {
			case 0, 1, 2, 3:
				pn := nextPN[id]
				size := 1 + rng.Intn(20)
				ae := rng.Intn(4) != 0
				switch rng.Intn(14) {
				case 0:
					size = 0
				case 1:
					size = 70000
				case 2:
					if pn > 0 {
						pn-- // non-increasing repeat
					}
				case 3:
					pn = -1
				case 4:
					id = 'X' // invalid space
				}
				fmt.Fprintf(&log, "op%d Send(now=%d sp=%c pn=%d size=%d ae=%v) -> ", op, now, id, pn, size, ae)
				errR := real.Send(now, id, pn, size, ae)
				errN := ref.Send(now, id, pn, size, ae)
				fmt.Fprintf(&log, "err=%v | basis: strict-pn + size/time validation, rejected call leaves state\n", errR)
				if !sameErr(errR, errN) {
					t.Fatalf("seed=%d Send err mismatch real=%v ref=%v\n%s", seed, errR, errN, log.String())
				}
				if errR == nil {
					nextPN[id] = pn + 1
					sentAny[id] = true
				}
			case 4, 5, 6:
				var pns []int64
				if sentAny[id] && rng.Intn(8) != 0 {
					hi := nextPN[id] - 1
					k := 1 + rng.Intn(4)
					seen := map[int64]bool{}
					for i := 0; i < k; i++ {
						pn := rng.Int63n(hi + 1)
						if !seen[pn] {
							pns = append(pns, pn)
							seen[pn] = true
						}
					}
				} else if rng.Intn(2) == 0 {
					pns = []int64{nextPN[id] + 5}
				}
				ackDelay := rng.Int63n(60) - 5
				fmt.Fprintf(&log, "op%d Ack(now=%d sp=%c pns=%v ackDelay=%d) -> ", op, now, id, pns, ackDelay)
				lostR, errR := real.Ack(now, id, pns, ackDelay)
				lostN, errN := ref.Ack(now, id, pns, ackDelay)
				fmt.Fprintf(&log, "lost=%v err=%v | basis: largest-newly RTT, ae ack clears ptoCount, then detect\n", lostR, errR)
				if !sameErr(errR, errN) || (errR == nil && !equalInt64(lostR, lostN)) {
					t.Fatalf("seed=%d Ack mismatch real=(%v,%v) ref=(%v,%v)\n%s", seed, lostR, errR, lostN, errN, log.String())
				}
			case 7:
				fmt.Fprintf(&log, "op%d Detect(now=%d sp=%c) -> ", op, now, id)
				lostR, errR := real.Detect(id, now)
				lostN, errN := ref.Detect(id, now)
				fmt.Fprintf(&log, "lost=%v err=%v | basis: la-pn>=3 or sentAt<=now-loss_delay (inclusive)\n", lostR, errR)
				if !sameErr(errR, errN) || (errR == nil && !equalInt64(lostR, lostN)) {
					t.Fatalf("seed=%d Detect mismatch\n%s", seed, log.String())
				}
			case 8:
				infoR, okR := real.Timer()
				infoN, okN := ref.timer()
				fmt.Fprintf(&log, "op%d Timer() -> (%+v,%v) ref=(%+v,%v) | basis: lt first (H wins ties), else lastAe+pto*2^min(count,20)\n",
					op, infoR, okR, infoN, okN)
				if okR != okN || (okR && infoR != infoN) {
					t.Fatalf("seed=%d Timer mismatch real=(%+v,%v) ref=(%+v,%v)\n%s", seed, infoR, okR, infoN, okN, log.String())
				}
				if okR && rng.Intn(2) == 0 {
					fireAt := infoR.Time
					if rng.Intn(5) == 0 {
						fireAt--
					}
					if fireAt > now {
						now = fireAt
					}
					fmt.Fprintf(&log, "op%d OnTimeout(now=%d) -> ", op, now)
					resR, errR := real.OnTimeout(now)
					resN, errN := ref.OnTimeout(now)
					if !sameErr(errR, errN) {
						t.Fatalf("seed=%d timeout err mismatch real=%v ref=%v\n%s", seed, errR, errN, log.String())
					}
					switch {
					case errR != nil:
						fmt.Fprintf(&log, "err=%v (state unchanged)\n", errR)
					case resR.Probe != nil:
						if resN.probe == nil || resR.Probe.Space != resN.probe.Space {
							t.Fatalf("seed=%d probe mismatch %+v vs %+v\n%s", seed, resR, resN, log.String())
						}
						fmt.Fprintf(&log, "probe=%c ptoCount incremented, no losses\n", resR.Probe.Space)
					default:
						if !equalInt64(resR.Loss, resN.lost) {
							t.Fatalf("seed=%d timeout loss mismatch %v vs %v\n%s", seed, resR.Loss, resN.lost, log.String())
						}
						fmt.Fprintf(&log, "loss=%v via re-Detect\n", resR.Loss)
					}
				}
			case 9:
				fmt.Fprintf(&log, "op%d HandshakeConfirmed(now=%d) -> ", op, now)
				errR := real.HandshakeConfirmed(now)
				errN := ref.Confirm(now)
				fmt.Fprintf(&log, "err=%v | basis: H dropped without losses, ptoCount=0, A joins PTO\n", errR)
				if !sameErr(errR, errN) {
					t.Fatalf("seed=%d confirm mismatch %v vs %v\n%s", seed, errR, errN, log.String())
				}
			}
			flush(op)
		}
	}
}

// TestConcurrentSafety exercises the serialized-entry contract; run with
// -race to confirm the mutex covers every shared-state path.
func TestConcurrentSafety(t *testing.T) {
	c, err := New(25)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(g*10000 + i)
				id := SpaceA
				if g%2 == 0 {
					id = SpaceH
				}
				pn := int64(g*1000 + i)
				_ = c.Send(now, id, pn, 10, i%2 == 0)
				_, _ = c.Ack(now+1, id, []int64{pn}, 0)
				_, _ = c.Timer()
				_, _ = c.Detect(id, now+2)
				_, _ = c.OnTimeout(now + 3)
				_, _ = c.Outstanding(id)
				_ = c.RTT()
				if i == 50 {
					_ = c.HandshakeConfirmed(now + 4)
				}
			}
		}(g)
	}
	wg.Wait()
}
