package canary

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const diffSequences = 1200
const diffEventsPerSequence = 60

type diffActor interface {
	start(int64) error
	reset(int64) error
	demote(int64) error
	route(string, int64) (RouteResult, error)
	observe(Version, bool, int64) error
	evaluate(int64) (EvalResult, error)
}

type prodActor struct{ s *Splitter }

func (a prodActor) start(t int64) error  { return a.s.Start(t) }
func (a prodActor) reset(t int64) error  { return a.s.Reset(t) }
func (a prodActor) demote(t int64) error { return a.s.Demote(t) }
func (a prodActor) route(id string, t int64) (RouteResult, error) {
	return a.s.Route(id, t)
}
func (a prodActor) observe(v Version, ok bool, t int64) error {
	return a.s.Observe(v, ok, t)
}
func (a prodActor) evaluate(t int64) (EvalResult, error) { return a.s.Evaluate(t) }

type naiveActor struct{ m *naiveModel }

func (a naiveActor) start(t int64) error  { return a.m.start(t) }
func (a naiveActor) reset(t int64) error  { return a.m.reset(t) }
func (a naiveActor) demote(t int64) error { return a.m.demote(t) }
func (a naiveActor) route(id string, t int64) (RouteResult, error) {
	return a.m.route(id, t)
}
func (a naiveActor) observe(v Version, ok bool, t int64) error {
	return a.m.observe(v, ok, t)
}
func (a naiveActor) evaluate(t int64) (EvalResult, error) { return a.m.evaluate(t) }

// idsPool holds ids partitioned by the production placement so sequences can
// intentionally exercise gray/stable membership while the oracle uses its own
// mapping for the raw-placement comparison (tested separately).
var (
	grayIDs []string
	allIDs  []string
)

func init() {
	for i := 0; len(grayIDs) < 40 || len(allIDs) < 120; i++ {
		id := fmt.Sprintf("diff-%d", i)
		allIDs = append(allIDs, id)
		if Placement(id) < 9000 {
			grayIDs = append(grayIDs, id)
		}
	}
}

func randomConfig(rng *rand.Rand) Config {
	n := 1 + rng.Intn(4)
	stages := make([]int, n)
	next := 1 + rng.Intn(500)
	for i := range stages {
		stages[i] = next
		if i < n-1 {
			next += 1 + rng.Intn((Basis-next)/(n-i))
			if next >= Basis {
				next = Basis - 1
			}
		} else {
			stages[i] = Basis
		}
	}
	return Config{
		Stages:              stages,
		MinDwell:            int64(rng.Intn(30)),
		MinGrayRequests:     rng.Intn(8),
		ErrorRateTolerance:  rng.Intn(500),
		MaxConsecutiveFails: 1 + rng.Intn(3),
		StickyTTL:           int64(rng.Intn(25)),
		MaxSticky:           1 + rng.Intn(6),
	}
}

func stickySnapshot(s *Splitter) map[string]naiveSticky {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]naiveSticky, s.sticky.Len())
	for id, elem := range s.index {
		e := elem.Value.(*stickyEntry)
		out[id] = naiveSticky{version: e.version, lastRouted: e.lastRouted}
	}
	return out
}

func compareState(t *testing.T, s *Splitter, m *naiveModel, log *strings.Builder, step string) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Fatalf("%s\n  state mismatch: %s\n%s", step, fmt.Sprintf(format, args...), log.String())
	}
	if s.Phase() != m.phase {
		fail("phase %v vs %v", s.Phase(), m.phase)
	}
	if s.Stage() != m.stage {
		fail("stage %d vs %d", s.Stage(), m.stage)
	}
	if s.Ratio() != m.ratio() {
		fail("ratio %d vs %d", s.Ratio(), m.ratio())
	}
	if s.FailStreak() != m.failStreak {
		fail("failStreak %d vs %d", s.FailStreak(), m.failStreak)
	}
	if s.StickyCount() != len(m.sticky) {
		fail("sticky count %d vs %d", s.StickyCount(), len(m.sticky))
	}
	got := stickySnapshot(s)
	for id, want := range m.sticky {
		have, ok := got[id]
		if !ok {
			fail("missing sticky %q", id)
		}
		if have != want {
			fail("sticky %q = %+v vs %+v", id, have, want)
		}
	}
	for id := range got {
		if _, ok := m.sticky[id]; !ok {
			fail("unexpected sticky %q", id)
		}
	}
	s.mu.Lock()
	gw := s.window
	s.mu.Unlock()
	if gw.grayTotal != m.grayTotal || gw.grayFail != m.grayFail ||
		gw.stableTotal != m.stableTotal || gw.stableFail != m.stableFail {
		fail("window %+v vs (%d,%d,%d,%d)", gw,
			m.grayTotal, m.grayFail, m.stableTotal, m.stableFail)
	}
}

func reasonForEvent(kind int) string {
	switch kind {
	case 0:
		return "manual start"
	case 1:
		return "manual reset"
	case 2:
		return "manual demote"
	case 3:
		return "route request"
	case 4:
		return "observe outcome"
	default:
		return "periodic evaluation"
	}
}

// TestDifferentialAgainstNaive runs >1000 random event sequences against the
// production splitter and the independent naive oracle, logging every input,
// output and decision basis (visible with -v or on failure).
func TestDifferentialAgainstNaive(t *testing.T) {
	for seq := 0; seq < diffSequences; seq++ {
		rng := rand.New(rand.NewSource(int64(0xC0FFEE + seq)))
		cfg := randomConfig(rng)
		s, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: %v", seq, err)
		}
		m := newNaive(cfg)
		prod := prodActor{s}
		oracle := naiveActor{m}

		var log strings.Builder
		fmt.Fprintf(&log, "seq %d cfg=%+v\n", seq, cfg)
		now := int64(0)

		for ev := 0; ev < diffEventsPerSequence; ev++ {
			kind := rng.Intn(6)
			// Occasionally jump backwards to exercise rejection ordering.
			now += int64(rng.Intn(40))
			if rng.Intn(8) == 0 {
				now -= int64(rng.Intn(20))
			}
			if now < 0 {
				now = 0
			}
			header := fmt.Sprintf("seq %d ev %d t=%d (%s)",
				seq, ev, now, reasonForEvent(kind))

			switch kind {
			case 0:
				e1, e2 := prod.start(now), oracle.start(now)
				fmt.Fprintf(&log, "  [%s] start -> %v | %v\n", header, e1, e2)
				if diffErr(e1, e2) {
					t.Fatalf("%s start err %v vs %v\n%s", header, e1, e2, log.String())
				}
			case 1:
				e1, e2 := prod.reset(now), oracle.reset(now)
				fmt.Fprintf(&log, "  [%s] reset -> %v | %v\n", header, e1, e2)
				if diffErr(e1, e2) {
					t.Fatalf("%s reset err %v vs %v\n%s", header, e1, e2, log.String())
				}
			case 2:
				e1, e2 := prod.demote(now), oracle.demote(now)
				fmt.Fprintf(&log, "  [%s] demote -> %v | %v\n", header, e1, e2)
				if diffErr(e1, e2) {
					t.Fatalf("%s demote err %v vs %v\n%s", header, e1, e2, log.String())
				}
			case 3:
				id := allIDs[rng.Intn(len(allIDs))]
				r1, e1 := prod.route(id, now)
				r2, e2 := oracle.route(id, now)
				basis := routeBasis(m, id, now, r2)
				fmt.Fprintf(&log, "  [%s] route(%q) -> %+v/%v | %+v/%v  basis: %s\n",
					header, id, r1, e1, r2, e2, basis)
				if diffErr(e1, e2) || r1 != r2 {
					t.Fatalf("%s route mismatch %+v/%v vs %+v/%v\n%s",
						header, r1, e1, r2, e2, log.String())
				}
			case 4:
				v := Version(rng.Intn(3) - 1) // sometimes invalid version -1/2
				if v < 0 || v > VersionGray {
					v = Version(2)
				}
				success := rng.Intn(2) == 0
				e1 := prod.observe(v, success, now)
				e2 := oracle.observe(v, success, now)
				fmt.Fprintf(&log, "  [%s] observe(v=%d ok=%v) -> %v | %v\n",
					header, v, success, e1, e2)
				if diffErr(e1, e2) {
					t.Fatalf("%s observe err %v vs %v\n%s", header, e1, e2, log.String())
				}
			case 5:
				r1, e1 := prod.evaluate(now)
				r2, e2 := oracle.evaluate(now)
				fmt.Fprintf(&log, "  [%s] evaluate -> %+v/%v | %+v/%v  basis: %s\n",
					header, r1, e1, r2, e2, evalBasis(m, now))
				if diffErr(e1, e2) || r1 != r2 {
					t.Fatalf("%s evaluate mismatch %+v/%v vs %+v/%v\n%s",
						header, r1, e1, r2, e2, log.String())
				}
			}
			compareState(t, s, m, &log, header)
		}
		// Emit the per-sequence trace under -v.
		t.Logf("\n%s", log.String())
	}
}

func diffErr(a, b error) bool {
	return (a == nil) != (b == nil) || (a != nil && a.Error() != b.Error())
}

func routeBasis(m *naiveModel, id string, now int64, r RouteResult) string {
	switch {
	case m.phase == PhaseRolledBack:
		return "rollback forces stable"
	case r.Source == SourceSticky:
		rec := m.sticky[id]
		return fmt.Sprintf("sticky valid: %d in [%d,%d)",
			now, rec.lastRouted, rec.lastRouted+m.cfg.StickyTTL)
	default:
		return fmt.Sprintf("ratio: pos=%d ratio=%d gray=%v",
			naivePlacement(id), m.ratio(), r.Version == VersionGray)
	}
}

func evalBasis(m *naiveModel, now int64) string {
	switch {
	case m.phase != PhaseRunning:
		return "not running"
	case now-m.enteredAt < m.cfg.MinDwell:
		return fmt.Sprintf("dwell %d < %d", now-m.enteredAt, m.cfg.MinDwell)
	case m.grayTotal < m.cfg.MinGrayRequests:
		return fmt.Sprintf("samples %d < %d", m.grayTotal, m.cfg.MinGrayRequests)
	default:
		return fmt.Sprintf("gray %d/%d vs stable %d/%d + tol %d",
			m.grayFail, m.grayTotal, m.stableFail, m.stableTotal,
			m.cfg.ErrorRateTolerance)
	}
}
