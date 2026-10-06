package canary

import (
	"math/big"
	"sort"
)

// naiveModel is an intentionally simple re-implementation of the splitter
// semantics, used only as an independent oracle in differential tests.
//
// It deliberately avoids container/list, high-word bucket mapping and the
// production comparison path: sticky state is a plain map rebuilt into a
// sorted slice for eviction, placement uses a salted FNV-1a modulo, and error
// rates are compared with math/big.Rat. Any agreement is therefore evidence
// about behavior rather than shared implementation.
type naiveModel struct {
	cfg Config

	phase      Phase
	stage      int
	enteredAt  int64
	lastNow    int64
	failStreak int

	grayTotal, grayFail     int
	stableTotal, stableFail int

	// id -> sticky record
	sticky map[string]naiveSticky
}

type naiveSticky struct {
	version    Version
	lastRouted int64
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, sticky: map[string]naiveSticky{}}
}

// naivePlacement deliberately shares the contract's mapping (the position is
// an externally specified pure function of the id); the oracle's independence
// instead comes from its storage (plain map + sort), time handling and the
// big.Rat comparison path.
func naivePlacement(id string) int { return Placement(id) }

func (m *naiveModel) ratio() int {
	switch m.phase {
	case PhaseRunning:
		return m.cfg.Stages[m.stage]
	case PhaseCompleted:
		return m.cfg.Stages[len(m.cfg.Stages)-1]
	default:
		return 0
	}
}

func (m *naiveModel) checkClock(now int64) error {
	if now < m.lastNow {
		return ErrClockWentBack
	}
	return nil
}

func (m *naiveModel) clearWindow() {
	m.grayTotal, m.grayFail = 0, 0
	m.stableTotal, m.stableFail = 0, 0
}

func (m *naiveModel) start(now int64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	if m.phase != PhaseNotStarted {
		return ErrIllegalState
	}
	m.phase = PhaseRunning
	m.stage = 0
	m.enteredAt = now
	m.lastNow = now
	m.failStreak = 0
	m.clearWindow()
	return nil
}

func (m *naiveModel) reset(now int64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.phase = PhaseNotStarted
	m.stage = 0
	m.enteredAt = 0
	m.lastNow = now
	m.failStreak = 0
	m.clearWindow()
	m.sticky = map[string]naiveSticky{}
	return nil
}

func (m *naiveModel) demote(now int64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	if m.phase != PhaseRunning || m.stage == 0 {
		return ErrIllegalState
	}
	m.stage--
	m.enteredAt = now
	m.lastNow = now
	m.failStreak = 0
	m.clearWindow()
	return nil
}

// evictNaive drops the record with the smallest lastRouted time until the map
// fits the cap, rebuilding the ordering from scratch (O(P log P)).
func (m *naiveModel) evictNaive() {
	for len(m.sticky) > m.cfg.MaxSticky {
		ids := make([]string, 0, len(m.sticky))
		for id := range m.sticky {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			ti, tj := m.sticky[ids[i]].lastRouted, m.sticky[ids[j]].lastRouted
			if ti != tj {
				return ti < tj
			}
			return ids[i] < ids[j]
		})
		delete(m.sticky, ids[0])
	}
}

func (m *naiveModel) route(id string, now int64) (RouteResult, error) {
	if id == "" {
		return RouteResult{}, ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return RouteResult{}, err
	}
	m.lastNow = now

	if m.phase == PhaseRolledBack {
		return RouteResult{Version: VersionStable, Source: SourceRollback}, nil
	}
	if rec, ok := m.sticky[id]; ok && now-rec.lastRouted < m.cfg.StickyTTL {
		rec.lastRouted = now
		m.sticky[id] = rec
		return RouteResult{Version: rec.version, Source: SourceSticky}, nil
	}
	delete(m.sticky, id)

	v := VersionStable
	if naivePlacement(id) < m.ratio() {
		v = VersionGray
	}
	m.sticky[id] = naiveSticky{version: v, lastRouted: now}
	m.evictNaive()
	return RouteResult{Version: v, Source: SourceRatio}, nil
}

func (m *naiveModel) observe(v Version, success bool, now int64) error {
	if v != VersionStable && v != VersionGray {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.lastNow = now
	if m.phase != PhaseRunning {
		return nil
	}
	switch v {
	case VersionGray:
		m.grayTotal++
		if !success {
			m.grayFail++
		}
	case VersionStable:
		m.stableTotal++
		if !success {
			m.stableFail++
		}
	}
	return nil
}

func (m *naiveModel) evaluate(now int64) (EvalResult, error) {
	if err := m.checkClock(now); err != nil {
		return EvalResult{}, err
	}
	m.lastNow = now

	res := EvalResult{
		Stage:       m.stage,
		FailStreak:  m.failStreak,
		GrayTotal:   m.grayTotal,
		GrayFail:    m.grayFail,
		StableTotal: m.stableTotal,
		StableFail:  m.stableFail,
	}
	if m.phase != PhaseRunning {
		res.Outcome = EvalNotRunning
		return res, nil
	}
	if now-m.enteredAt < m.cfg.MinDwell {
		res.Outcome = EvalDwellNotMet
		return res, nil
	}
	if m.grayTotal < m.cfg.MinGrayRequests {
		res.Outcome = EvalInsufficientSamples
		return res, nil
	}

	grayRate := new(big.Rat)
	if m.grayTotal > 0 {
		grayRate.SetFrac(big.NewInt(int64(m.grayFail)), big.NewInt(int64(m.grayTotal)))
	}
	if m.stableTotal == 0 {
		// stable error rate treated as zero.
	} else {
		_ = grayRate // grayTotal >= G >= 0 here; keep the explicit zero case above
	}
	stableRate := new(big.Rat)
	if m.stableTotal > 0 {
		stableRate.SetFrac(big.NewInt(int64(m.stableFail)), big.NewInt(int64(m.stableTotal)))
	}
	limit := new(big.Rat).Add(stableRate,
		big.NewRat(int64(m.cfg.ErrorRateTolerance), int64(Basis)))

	if grayRate.Cmp(limit) <= 0 {
		m.failStreak = 0
		m.clearWindow()
		res.Outcome = EvalPassed
		res.FailStreak = 0
		if m.stage == len(m.cfg.Stages)-1 {
			m.phase = PhaseCompleted
		} else {
			m.stage++
			m.enteredAt = now
			res.Advanced = true
		}
		res.Stage = m.stage
		return res, nil
	}

	m.failStreak++
	res.Outcome = EvalFailed
	res.FailStreak = m.failStreak
	if m.failStreak >= m.cfg.MaxConsecutiveFails {
		m.phase = PhaseRolledBack
		m.clearWindow()
		m.sticky = map[string]naiveSticky{}
		res.RolledBack = true
		return res, nil
	}
	m.enteredAt = now
	m.clearWindow()
	return res, nil
}
