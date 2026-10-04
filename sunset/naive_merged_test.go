package sunset

import (
	"errors"
	"sort"
)

// naiveModel 是逐消费者全量扫描的独立朴素实现，作为正确性对照。
type naiveModel struct {
	cfg      Config
	lastNow  int64
	phase    map[string]int
	sunset   map[string]int64
	brown    map[string]int64
	cnt      map[string]int
	sum      map[string]int64
	children map[string][]string
	last     map[string]map[string]int64
	ackAt    map[string]map[string]int64
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg: cfg, lastNow: -1,
		phase: map[string]int{}, sunset: map[string]int64{}, brown: map[string]int64{},
		cnt: map[string]int{}, sum: map[string]int64{},
		children: map[string][]string{},
		last:     map[string]map[string]int64{}, ackAt: map[string]map[string]int64{},
	}
}

func (m *naiveModel) activeConsumers(d string, now int64) []string {
	var out []string
	for c, la := range m.last[d] {
		if la > now-m.cfg.Q {
			if ac, acked := m.ackAt[d][c]; !acked || ac < la {
				out = append(out, c)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (m *naiveModel) unretiredChildren(d string) []string {
	var out []string
	for _, ch := range m.children[d] {
		if m.phase[ch] != 3 {
			out = append(out, ch)
		}
	}
	sort.Strings(out)
	return out
}

func sentinelName(err error) string {
	pairs := []struct {
		e    error
		name string
	}{
		{ErrInvalidArg, "InvalidArg"}, {ErrClockBack, "ClockBack"},
		{ErrNotFound, "NotFound"}, {ErrPhase, "Phase"},
		{ErrNoticeTooShort, "NoticeTooShort"}, {ErrTooEarly, "TooEarly"},
		{ErrDownstream, "Downstream"}, {ErrConsumers, "Consumers"},
		{ErrNotConsumer, "NotConsumer"}, {ErrExtendLimit, "ExtendLimit"},
		{ErrTooLong, "TooLong"}, {ErrBrownout, "Brownout"}, {ErrRetired, "Retired"},
	}
	for _, p := range pairs {
		if errors.Is(err, p.e) {
			return p.name
		}
	}
	if err != nil {
		return "Other"
	}
	return ""
}

// op 编码：0 Add 1 Deprecate 2 Undeprecate 3 Access 4 Ack 5 Advance 6 Extend。
type op struct {
	kind    int
	now     int64
	d, c    string
	arg     int64
	parents []string
}

type opResult struct {
	allowed bool
	err     string
	items   []string
}

func kindName(k int) string {
	return []string{"Add", "Deprecate", "Undeprecate", "Access", "Ack", "Advance", "Extend"}[k]
}

func (m *naiveModel) apply(o op) opResult {
	r := opResult{}
	checkClock := func() bool {
		if o.now < 0 || o.now > 1_000_000_000_000 {
			r.err = "InvalidArg"
			return false
		}
		if o.now < m.lastNow {
			r.err = "ClockBack"
			return false
		}
		return true
	}
	switch o.kind {
	case 0:
		if o.d == "" {
			r.err = "InvalidArg"
			return r
		}
		if !checkClock() {
			return r
		}
		if _, ok := m.phase[o.d]; ok {
			r.err = "Other"
			return r
		}
		if len(o.parents) > 8 {
			r.err = "InvalidArg"
			return r
		}
		for _, p := range o.parents {
			if _, ok := m.phase[p]; !ok {
				r.err = "InvalidArg"
				return r
			}
		}
		m.phase[o.d] = 0
		for _, p := range o.parents {
			m.children[p] = append(m.children[p], o.d)
		}
		m.lastNow = o.now
	case 1:
		if o.d == "" || o.arg < 0 {
			r.err = "InvalidArg"
			return r
		}
		if !checkClock() {
			return r
		}
		ph, ok := m.phase[o.d]
		if !ok {
			r.err = "NotFound"
			return r
		}
		if ph != 0 {
			r.err = "Phase"
			return r
		}
		if o.arg < m.cfg.Nmin {
			r.err = "NoticeTooShort"
			return r
		}
		m.phase[o.d] = 1
		m.sunset[o.d] = o.now + o.arg
		m.brown[o.d] = m.sunset[o.d] - m.cfg.Bw
		m.lastNow = o.now
	case 2:
		if o.d == "" {
			r.err = "InvalidArg"
			return r
		}
		if !checkClock() {
			return r
		}
		if _, ok := m.phase[o.d]; !ok {
			r.err = "NotFound"
			return r
		}
		if m.phase[o.d] != 1 {
			r.err = "Phase"
			return r
		}
		m.phase[o.d] = 0
		m.lastNow = o.now
	case 3:
		if o.c == "" || o.d == "" {
			r.err = "InvalidArg"
			return r
		}
		if !checkClock() {
			return r
		}
		ph, ok := m.phase[o.d]
		if !ok {
			r.err = "NotFound"
			return r
		}
		switch ph {
		case 0, 1:
			r.allowed = true
		case 3:
			r.err = "Retired"
			return r
		default:
			delta := o.now - m.brown[o.d]
			i, off := delta/m.cfg.Pd, delta%m.cfg.Pd
			shut := (i + 1) * m.cfg.X
			if shut > m.cfg.Pd {
				shut = m.cfg.Pd
			}
			if off < shut {
				r.err = "Brownout"
				return r
			}
			r.allowed = true
		}
		if m.last[o.d] == nil {
			m.last[o.d] = map[string]int64{}
		}
		m.last[o.d][o.c] = o.now
		m.lastNow = o.now
	case 4:
		if o.c == "" || o.d == "" {
			r.err = "InvalidArg"
			return r
		}
		if !checkClock() {
			return r
		}
		if _, ok := m.phase[o.d]; !ok {
			r.err = "NotFound"
			return r
		}
		if _, ok := m.last[o.d][o.c]; !ok {
			r.err = "NotConsumer"
			return r
		}
		if m.ackAt[o.d] == nil {
			m.ackAt[o.d] = map[string]int64{}
		}
		m.ackAt[o.d][o.c] = o.now
		m.lastNow = o.now
	case 5:
		if o.d == "" {
			r.err = "InvalidArg"
			return r
		}
		if !checkClock() {
			return r
		}
		ph, ok := m.phase[o.d]
		if !ok {
			r.err = "NotFound"
			return r
		}
		switch ph {
		case 0, 3:
			r.err = "Phase"
		case 1:
			if o.now < m.brown[o.d] {
				r.err = "TooEarly"
				return r
			}
			m.phase[o.d] = 2
			m.lastNow = o.now
		default:
			if o.now < m.sunset[o.d] {
				r.err = "TooEarly"
				return r
			}
			if blk := m.unretiredChildren(o.d); len(blk) > 0 {
				r.err, r.items = "Downstream", blk
				return r
			}
			if act := m.activeConsumers(o.d, o.now); len(act) > 0 {
				r.err, r.items = "Consumers", act
				return r
			}
			m.phase[o.d] = 3
			m.lastNow = o.now
		}
	case 6:
		if o.d == "" || o.c == "" || o.arg <= 0 {
			r.err = "InvalidArg"
			return r
		}
		if !checkClock() {
			return r
		}
		ph, ok := m.phase[o.d]
		if !ok {
			r.err = "NotFound"
			return r
		}
		if ph != 1 {
			r.err = "Phase"
			return r
		}
		act := m.activeConsumers(o.d, o.now)
		if idx := sort.SearchStrings(act, o.c); idx == len(act) || act[idx] != o.c {
			r.err = "NotConsumer"
			return r
		}
		if m.cnt[o.d] >= 2 {
			r.err = "ExtendLimit"
			return r
		}
		if m.sum[o.d]+o.arg > m.cfg.Xmax {
			r.err = "TooLong"
			return r
		}
		m.cnt[o.d]++
		m.sum[o.d] += o.arg
		m.sunset[o.d] += o.arg
		m.brown[o.d] += o.arg
		m.lastNow = o.now
	}
	return r
}
