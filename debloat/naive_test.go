package debloat

import "math/big"

// naiveModel is a line-by-line restatement of the specification. Its window
// is a plain slice of (bytes, dt) pairs and every throughput value is
// recomputed by summing the whole window. It exists only to cross-check the
// production implementation in randomized tests.
type naiveSample struct {
	bytes uint64
	dt    uint64
}

type naiveModel struct {
	cfg Config

	cur      uint64
	chans    int
	window   []naiveSample
	streak   int
	pol      bool
	paused   bool
	lastDir  Direction
	gap      int
	damp     int
	applied  int
	forced   int
	skipped  int
	windowOp int
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, cur: cfg.B0, chans: cfg.C0, lastDir: DirNone}
}

func (m *naiveModel) beff(channels int) uint64 {
	return effectiveCap(m.cfg, channels)
}

// recordChange mirrors the per-change direction/gap/damp rule and reports
// whether damp was (re)armed on this very change.
func (m *naiveModel) recordChange(dir Direction) bool {
	set := false
	if m.lastDir != DirNone && dir != m.lastDir && m.gap <= m.cfg.H {
		m.damp = m.cfg.H
		set = true
	}
	m.lastDir = dir
	m.gap = 0
	return set
}

type naiveResult struct {
	ok     bool
	action Action
	cur    uint64
	r      uint64
	cand   uint64
	streak int
}

func (m *naiveModel) sample(bytes, dt uint64) naiveResult {
	if bytes > (uint64(1)<<30) || dt < 1 || dt > 1_000_000 {
		return naiveResult{ok: false}
	}
	if m.paused {
		return naiveResult{ok: false}
	}
	if m.pol {
		m.pol = false
		m.skipped++
		return naiveResult{ok: true, action: ActionSkipped, cur: m.cur, streak: m.streak}
	}

	if m.lastDir != DirNone {
		m.gap++
	}

	// Window insert with naive ops counting.
	if len(m.window) == m.cfg.W {
		m.window = m.window[1:]
		m.windowOp += 2
	} else {
		m.windowOp++
	}
	m.window = append(m.window, naiveSample{bytes, dt})

	// Naive: re-sum the whole window every time, using big.Int throughout.
	var sb, sd big.Int
	for _, s := range m.window {
		sb.Add(&sb, new(big.Int).SetUint64(s.bytes))
		sd.Add(&sd, new(big.Int).SetUint64(s.dt))
	}
	rb := new(big.Int).Mul(&sb, big.NewInt(1000))
	rb.Quo(rb, &sd)
	db := new(big.Int).Mul(rb, new(big.Int).SetUint64(m.cfg.T))
	db.Add(db, big.NewInt(999))
	db.Quo(db, big.NewInt(1000))
	pb := new(big.Int).Add(db, big.NewInt(int64(m.chans)-1))
	pb.Quo(pb, big.NewInt(int64(m.chans)))

	r := rb.Uint64()
	beff := m.beff(m.chans)
	raw := pb.Uint64()
	if raw < m.cfg.Bmin {
		raw = m.cfg.Bmin
	}
	if raw > beff {
		raw = beff
	}
	cand := raw / m.cfg.G * m.cfg.G

	need := m.cfg.Kc
	if m.damp > 0 {
		need = 2 * m.cfg.Kc
	}
	dampSet := false
	action := ActionHold

	switch {
	case cand == m.cur:
		m.streak = 0
	case cand > m.cur:
		if (cand-m.cur)*100 >= m.cur*m.cfg.ThU || cand == beff {
			m.streak++
			if m.streak >= need {
				m.cur = cand
				m.streak = 0
				m.pol = true
				m.applied++
				dampSet = m.recordChange(DirUp)
				action = ActionApplied
			} else {
				action = ActionPending
			}
		} else {
			m.streak = 0
		}
	default:
		if (m.cur-cand)*100 >= m.cur*m.cfg.ThD || cand == m.cfg.Bmin {
			m.cur = cand
			m.streak = 0
			m.pol = true
			m.applied++
			dampSet = m.recordChange(DirDown)
			action = ActionApplied
		} else {
			m.streak = 0
		}
	}

	if m.damp > 0 && !dampSet {
		m.damp--
	}

	return naiveResult{ok: true, action: action, cur: m.cur, r: r, cand: cand, streak: m.streak}
}

func (m *naiveModel) setChannels(channels int) (rejected bool, reason error) {
	if channels < 1 || channels > 10_000 {
		return true, ErrIllegalArgument
	}
	if channels == m.chans {
		return false, nil
	}
	be := m.beff(channels)
	if be < m.cfg.Bmin {
		return true, ErrInsufficientCapacity
	}
	m.chans = channels
	m.streak = 0
	if m.cur > be {
		m.cur = be
		m.pol = true
		m.forced++
		m.recordChange(DirDown)
	}
	return false, nil
}

func (m *naiveModel) pause() { m.paused = true }

func (m *naiveModel) resume() {
	m.paused = false
	m.window = nil
	m.streak = 0
}

func (m *naiveModel) snapshot() Snapshot {
	return Snapshot{
		Cur:          m.cur,
		Channels:     m.chans,
		WindowLen:    len(m.window),
		Streak:       m.streak,
		Polluted:     m.pol,
		Paused:       m.paused,
		LastDir:      m.lastDir,
		Gap:          m.gap,
		Damp:         m.damp,
		AppliedCount: m.applied,
		ForcedCount:  m.forced,
		SkippedCount: m.skipped,
		WindowOps:    m.windowOp,
	}
}
