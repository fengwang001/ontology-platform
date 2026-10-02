package quicloss

import (
	"sort"
)

// refPacket / refSpace / refModel are a from-scratch naive transcription of
// the specification, written independently from the production Controller.
// The differential test feeds identical random operation sequences to both.

type refPacket struct {
	send int64
	size int
	ae   bool
}

type refSpace struct {
	pkts    map[int64]refPacket
	la      *int64
	lt      *int64
	maxSent *int64
	lastAe  *int64
	dropped bool
}

type refModel struct {
	mad       int64
	latest    int64
	srtt      int64
	rttvar    int64
	minRtt    *int64
	hasSample bool
	ptoCount  int
	confirmed bool
	lastNow   *int64
	sp        map[Space]*refSpace
}

func newRefModel(mad int64) *refModel {
	m := &refModel{
		mad:    mad,
		latest: 333,
		srtt:   333,
		rttvar: 166,
		sp:     map[Space]*refSpace{},
	}
	m.sp[SpaceHandshake] = &refSpace{pkts: map[int64]refPacket{}}
	m.sp[SpaceApp] = &refSpace{pkts: map[int64]refPacket{}}
	return m
}

type outcome struct {
	ok      bool
	reason  string
	lost    []int64
	timer   *TimerInfo
	timeout *TimeoutResult
}

func refBad(reason string) outcome { return outcome{ok: false, reason: reason} }

func validTime(t int64) bool { return t >= 0 && t <= 1_000_000_000_000 }

func (m *refModel) checkClock(now int64) bool {
	return m.lastNow == nil || now >= *m.lastNow
}

func (m *refModel) send(now int64, sp Space, pn int64, size int, ae bool) outcome {
	if sp != SpaceHandshake && sp != SpaceApp {
		return refBad("invalid space")
	}
	if size < 1 || size > 65535 {
		return refBad("invalid size")
	}
	if !validTime(now) {
		return refBad("invalid time")
	}
	if pn < 0 {
		return refBad("negative pn")
	}
	if !m.checkClock(now) {
		return refBad("clock backward")
	}
	s := m.sp[sp]
	if sp == SpaceHandshake && s.dropped {
		return refBad("space discarded")
	}
	if s.maxSent != nil && pn <= *s.maxSent {
		return refBad("pn not increasing")
	}
	s.pkts[pn] = refPacket{send: now, size: size, ae: ae}
	v := pn
	s.maxSent = &v
	if ae {
		t := now
		s.lastAe = &t
	}
	n := now
	m.lastNow = &n
	return outcome{ok: true}
}

func (m *refModel) ack(now int64, sp Space, pns []int64, delay int64) outcome {
	if sp != SpaceHandshake && sp != SpaceApp {
		return refBad("invalid space")
	}
	if !validTime(now) {
		return refBad("invalid time")
	}
	if len(pns) == 0 {
		return refBad("empty ack")
	}
	for _, pn := range pns {
		if pn < 0 {
			return refBad("negative pn")
		}
	}
	if delay < 0 {
		return refBad("invalid ack delay")
	}
	if !m.checkClock(now) {
		return refBad("clock backward")
	}
	s := m.sp[sp]
	if sp == SpaceHandshake && s.dropped {
		return refBad("space discarded")
	}
	for _, pn := range pns {
		if s.maxSent == nil || pn > *s.maxSent {
			return refBad("acked never sent")
		}
	}
	n := now
	m.lastNow = &n

	seen := map[int64]bool{}
	type np struct {
		pn int64
		p  refPacket
	}
	var newly []np
	for _, pn := range pns {
		if seen[pn] {
			continue
		}
		seen[pn] = true
		if p, ok := s.pkts[pn]; ok {
			newly = append(newly, np{pn, p})
		}
	}
	if len(newly) == 0 {
		return outcome{ok: true, lost: []int64{}}
	}

	var L np
	for _, x := range newly {
		if L.p == (refPacket{}) || x.pn > L.pn {
			L = x
		}
	}
	la := L.pn
	if s.la == nil || la > *s.la {
		s.la = &la
	}

	if L.p.ae {
		sample := now - L.p.send
		if sample < 1 {
			sample = 1
		}
		m.latest = sample
		if m.minRtt == nil || sample < *m.minRtt {
			v := sample
			m.minRtt = &v
		}
		if !m.hasSample {
			m.srtt = sample
			m.rttvar = sample / 2
			m.hasSample = true
		} else {
			var ad int64
			if sp == SpaceApp && m.confirmed {
				ad = delay
				if m.mad < ad {
					ad = m.mad
				}
			}
			var adj int64
			if m.latest < *m.minRtt+ad {
				adj = m.latest
			} else {
				adj = m.latest - ad
			}
			d := m.srtt - adj
			if d < 0 {
				d = -d
			}
			m.rttvar = (3*m.rttvar + d) / 4
			m.srtt = (7*m.srtt + adj) / 8
		}
	}

	anyAe := false
	for _, x := range newly {
		if x.p.ae {
			anyAe = true
		}
		delete(s.pkts, x.pn)
	}
	if anyAe {
		m.ptoCount = 0
	}
	lost := m.detect(sp, now)
	return outcome{ok: true, lost: lost}
}

func (m *refModel) detect(sp Space, now int64) []int64 {
	s := m.sp[sp]
	s.lt = nil
	lost := []int64{}
	if s.la == nil {
		return lost
	}
	la := *s.la
	base := m.latest
	if m.srtt > base {
		base = m.srtt
	}
	delay := 9 * base / 8
	if delay < 1 {
		delay = 1
	}
	var pns []int64
	for pn := range s.pkts {
		pns = append(pns, pn)
	}
	sort.Slice(pns, func(i, j int) bool { return pns[i] < pns[j] })
	for _, pn := range pns {
		if pn >= la {
			continue
		}
		p := s.pkts[pn]
		if la-pn >= 3 || p.send <= now-delay {
			lost = append(lost, pn)
			delete(s.pkts, pn)
			continue
		}
		lt := p.send + delay
		if s.lt == nil || lt < *s.lt {
			s.lt = &lt
		}
	}
	s.lastAe = nil
	for _, p := range s.pkts {
		if !p.ae {
			continue
		}
		if s.lastAe == nil || p.send > *s.lastAe {
			v := p.send
			s.lastAe = &v
		}
	}
	return lost
}

func (m *refModel) timer() *TimerInfo {
	// Loss first, tie -> H.
	if !m.sp[SpaceHandshake].dropped && m.sp[SpaceHandshake].lt != nil {
		if m.sp[SpaceApp].lt == nil || *m.sp[SpaceHandshake].lt <= *m.sp[SpaceApp].lt {
			return &TimerInfo{Time: *m.sp[SpaceHandshake].lt, Mode: ModeLoss, Space: SpaceHandshake}
		}
	}
	if m.sp[SpaceApp].lt != nil {
		if m.sp[SpaceHandshake].dropped || m.sp[SpaceHandshake].lt == nil || *m.sp[SpaceApp].lt < *m.sp[SpaceHandshake].lt {
			return &TimerInfo{Time: *m.sp[SpaceApp].lt, Mode: ModeLoss, Space: SpaceApp}
		}
	}
	var best *TimerInfo
	consider := func(sp Space, eligible bool) {
		s := m.sp[sp]
		if !eligible || s.lastAe == nil {
			return
		}
		pto := m.srtt
		if 4*m.rttvar > 1 {
			pto += 4 * m.rttvar
		} else {
			pto++
		}
		if sp == SpaceApp {
			pto += m.mad
		}
		shift := m.ptoCount
		if shift > 20 {
			shift = 20
		}
		at := *s.lastAe + pto*(int64(1)<<uint(shift))
		if best == nil || at < best.Time || (at == best.Time && sp == SpaceHandshake) {
			best = &TimerInfo{Time: at, Mode: ModePTO, Space: sp}
		}
	}
	consider(SpaceHandshake, !m.sp[SpaceHandshake].dropped)
	consider(SpaceApp, m.confirmed)
	return best
}

func (m *refModel) timeout(now int64) outcome {
	if !validTime(now) {
		return refBad("invalid time")
	}
	if !m.checkClock(now) {
		return refBad("clock backward")
	}
	tm := m.timer()
	if tm == nil || now < tm.Time {
		return refBad("timeout early")
	}
	n := now
	m.lastNow = &n
	if tm.Mode == ModeLoss {
		lost := m.detect(tm.Space, now)
		return outcome{ok: true, timeout: &TimeoutResult{Mode: ModeLoss, Space: tm.Space, LostPN: lost}}
	}
	m.ptoCount++
	return outcome{ok: true, timeout: &TimeoutResult{
		Mode:   ModePTO,
		Space:  tm.Space,
		LostPN: []int64{},
		Probe:  &ProbeRequest{Space: tm.Space, Count: m.ptoCount},
	}}
}

func (m *refModel) confirm(now int64) outcome {
	if !validTime(now) {
		return refBad("invalid time")
	}
	if !m.checkClock(now) {
		return refBad("clock backward")
	}
	n := now
	m.lastNow = &n
	m.confirmed = true
	m.sp[SpaceHandshake] = &refSpace{pkts: map[int64]refPacket{}, dropped: true}
	m.ptoCount = 0
	return outcome{ok: true}
}

func (m *refModel) detectCall(sp Space, now int64) outcome {
	if sp != SpaceHandshake && sp != SpaceApp {
		return refBad("invalid space")
	}
	if !validTime(now) {
		return refBad("invalid time")
	}
	if !m.checkClock(now) {
		return refBad("clock backward")
	}
	if sp == SpaceHandshake && m.sp[sp].dropped {
		return refBad("space discarded")
	}
	n := now
	m.lastNow = &n
	return outcome{ok: true, lost: m.detect(sp, now)}
}
