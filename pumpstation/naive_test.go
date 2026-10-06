package pumpstation

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type naivePump struct {
	state         PumpState
	running       bool
	cumulative    int64
	startedAt     int64
	stoppedAt     int64
	lastStartedAt int64
}

type naiveModel struct {
	cfg       Config
	pumps     []naivePump
	now       int64
	level     int
	levelSeen bool
	target    int
	dryLock   bool
	lastStart int64
	hasStart  bool
}

func newNaiveModel(cfg Config) *naiveModel {
	pumps := make([]naivePump, cfg.PumpCount)
	for i := range pumps {
		pumps[i].state = PumpAvailable
	}
	return &naiveModel{cfg: cfg, pumps: pumps}
}

func (m *naiveModel) total(p *naivePump, now int64) int64 {
	total := p.cumulative
	if p.running {
		total += now - p.startedAt
	}
	return total
}

func (m *naiveModel) stop(index int, now int64, reason ActionReason) Action {
	p := &m.pumps[index]
	p.cumulative = m.total(p, now)
	p.running = false
	p.stoppedAt = now
	return Action{PumpID: index + 1, Kind: ActionStop, Reason: reason}
}

func (m *naiveModel) start(index int, now int64, reason ActionReason) Action {
	p := &m.pumps[index]
	p.running = true
	p.startedAt = now
	p.lastStartedAt = now
	m.hasStart = true
	m.lastStart = now
	return Action{PumpID: index + 1, Kind: ActionStart, Reason: reason}
}

func (m *naiveModel) available() int {
	count := 0
	for i := range m.pumps {
		if m.pumps[i].state == PumpAvailable {
			count++
		}
	}
	return count
}

func (m *naiveModel) clampTarget() {
	available := 0
	for i := range m.pumps {
		if m.pumps[i].state == PumpAvailable {
			available++
		}
	}
	if m.target > available {
		m.target = available
	}
}

func (m *naiveModel) chooseStart(now int64) int {
	choice := -1
	for i := range m.pumps {
		p := &m.pumps[i]
		if p.state != PumpAvailable || p.running || now-p.stoppedAt < m.cfg.MinimumStopDuration {
			continue
		}
		if m.hasStart && now-m.lastStart < m.cfg.MinimumStartInterval {
			continue
		}
		if choice < 0 || m.total(p, now) < m.total(&m.pumps[choice], now) {
			choice = i
		}
	}
	return choice
}

func (m *naiveModel) chooseStop(now int64) int {
	choice := -1
	for i := range m.pumps {
		p := &m.pumps[i]
		if p.state != PumpAvailable || !p.running || now-p.startedAt < m.cfg.MinimumRunDuration {
			continue
		}
		if choice < 0 || m.total(p, now) >= m.total(&m.pumps[choice], now) {
			choice = i
		}
	}
	return choice
}

func (m *naiveModel) chooseMaintenanceStop(now int64) int {
	for i := range m.pumps {
		p := &m.pumps[i]
		if p.state == PumpMaintenance && p.running && now-p.startedAt >= m.cfg.MinimumRunDuration {
			return i
		}
	}
	return -1
}

func (m *naiveModel) runningIDs() []int {
	ids := make([]int, 0)
	for i := range m.pumps {
		if m.pumps[i].running {
			ids = append(ids, i+1)
		}
	}
	return ids
}

func (m *naiveModel) targetFor(level int, available int) int {
	lower := 0
	for i, start := range m.cfg.StartLevels {
		if level >= start {
			lower = i + 1
		}
	}
	upper := m.cfg.PumpCount
	for i, stop := range m.cfg.StopLevels {
		if level <= stop {
			upper = i
			break
		}
	}
	target := m.target
	if target < lower {
		target = lower
	}
	if target > upper {
		target = upper
	}
	if target > available {
		target = available
	}
	return target
}

func (m *naiveModel) report(now int64, level int) Evaluation {
	m.now = now
	m.level = level
	m.levelSeen = true
	actions := []Action{}
	if level <= m.cfg.DryRunLevel {
		m.dryLock = true
		m.target = 0
		for i := range m.pumps {
			if m.pumps[i].running {
				actions = append(actions, m.stop(i, now, ReasonDryRun))
			}
		}
		return Evaluation{Time: now, Level: level, Target: 0, DryLock: true, Running: m.runningIDs(), Actions: actions}
	}
	if m.dryLock && level > m.cfg.DryRunRecoveryLevel {
		m.dryLock = false
		m.target = 0
	}
	if m.dryLock {
		return Evaluation{Time: now, Level: level, Target: 0, DryLock: true, Running: m.runningIDs(), Actions: actions}
	}
	available := m.available()
	m.target = m.targetFor(level, available)
	if level >= m.cfg.OverflowLevel {
		m.target = available
		for i := range m.pumps {
			if m.pumps[i].state == PumpAvailable && !m.pumps[i].running {
				actions = append(actions, m.start(i, now, ReasonOverflow))
			}
		}
	} else {
		if index := m.chooseMaintenanceStop(now); index >= 0 {
			actions = append(actions, m.stop(index, now, ReasonMaintenanceStop))
		} else {
			running := len(m.runningIDs())
			if running < m.target {
				if index := m.chooseStart(now); index >= 0 {
					actions = append(actions, m.start(index, now, ReasonNormalStart))
				}
			} else if running > m.target {
				if index := m.chooseStop(now); index >= 0 {
					actions = append(actions, m.stop(index, now, ReasonNormalStop))
				}
			}
		}
	}
	return Evaluation{Time: now, Level: level, Target: m.target, DryLock: false, Running: m.runningIDs(), Actions: actions}
}

func (m *naiveModel) setFault(now int64, pumpID int, faulted bool) StateChange {
	m.now = now
	index := pumpID - 1
	p := &m.pumps[index]
	actions := []Action{}
	if faulted {
		if p.running {
			actions = append(actions, m.stop(index, now, ReasonFault))
		}
		p.state = PumpFaulted
	} else {
		p.state = PumpAvailable
		p.running = false
		p.stoppedAt = now
	}
	m.clampTarget()
	return StateChange{Time: now, PumpID: pumpID, State: p.state, Actions: actions}
}

func (m *naiveModel) setMaintenance(now int64, pumpID int, maintenance bool) StateChange {
	m.now = now
	index := pumpID - 1
	p := &m.pumps[index]
	actions := []Action{}
	if maintenance {
		p.state = PumpMaintenance
		if p.running && (m.dryLock || now-p.startedAt >= m.cfg.MinimumRunDuration) {
			reason := ReasonMaintenanceStop
			if m.dryLock {
				reason = ReasonMaintenance
			}
			actions = append(actions, m.stop(index, now, reason))
		}
	} else {
		p.state = PumpAvailable
	}
	m.clampTarget()
	return StateChange{Time: now, PumpID: pumpID, State: p.state, Actions: actions}
}

type randomOp struct {
	kind        int
	now         int64
	level       int
	pump        int
	maintenance bool
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	levels := []int{0, 10, 20, 21, 70, 80, 85, 90, 100, 120, 140, 145, 199, 200}
	for seed := int64(1); seed <= 80; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{
				PumpCount:            2 + rng.Intn(3),
				StartLevels:          []int{100, 120, 140, 160},
				StopLevels:           []int{70, 80, 90, 100},
				DryRunLevel:          10,
				DryRunRecoveryLevel:  20,
				OverflowLevel:        200,
				MinimumRunDuration:   int64(2 + rng.Intn(8)),
				MinimumStopDuration:  int64(3 + rng.Intn(8)),
				MinimumStartInterval: int64(4 + rng.Intn(8)),
			}
			cfg.StartLevels = cfg.StartLevels[:cfg.PumpCount]
			cfg.StopLevels = cfg.StopLevels[:cfg.PumpCount]
			controller, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			naive := newNaiveModel(cfg)
			now := int64(0)
			for step := 0; step < 220; step++ {
				now += int64(1 + rng.Intn(6))
				pump := 1 + rng.Intn(cfg.PumpCount)
				level := levels[rng.Intn(len(levels))]
				op := randomOp{kind: rng.Intn(3), now: now, level: level, pump: pump, maintenance: rng.Intn(2) == 0}
				state := naive.pumps[pump-1].state
				if op.kind == 1 {
					op.maintenance = state != PumpFaulted
				}
				if op.kind == 2 {
					if state == PumpFaulted {
						op.kind = 0
					} else {
						op.maintenance = state == PumpAvailable
					}
				}
				t.Logf("RANDOM seed=%d step=%d input=%+v", seed, step, op)
				switch op.kind {
				case 0:
					got, err := controller.ReportLevel(op.now, op.level)
					want := naive.report(op.now, op.level)
					if err != nil {
						t.Fatalf("report rejected: %v", err)
					}
					if !reflect.DeepEqual(got.Actions, want.Actions) || got.Target != want.Target || got.DryLock != want.DryLock || !reflect.DeepEqual(got.Running, want.Running) {
						t.Fatalf("evaluation mismatch\ngot=%+v\nwant=%+v", got, want)
					}
				case 1:
					got, gerr := controller.SetFault(op.now, op.pump, op.maintenance)
					want := naive.setFault(op.now, op.pump, op.maintenance)
					if gerr != nil {
						t.Fatalf("fault op rejected: %v", gerr)
					}
					if !reflect.DeepEqual(got.Actions, want.Actions) || got.State != want.State {
						t.Fatalf("fault mismatch got=%+v want=%+v", got, want)
					}
				default:
					got, gerr := controller.SetMaintenance(op.now, op.pump, op.maintenance)
					want := naive.setMaintenance(op.now, op.pump, op.maintenance)
					if gerr != nil {
						t.Fatalf("maintenance op rejected: %v", gerr)
					}
					if !reflect.DeepEqual(got.Actions, want.Actions) || got.State != want.State {
						t.Fatalf("maintenance mismatch got=%+v want=%+v", got, want)
					}
				}
				assertSnapshotMatchesNaive(t, controller, naive)
			}
		})
	}
}

func assertSnapshotMatchesNaive(t *testing.T, controller *Controller, naive *naiveModel) {
	t.Helper()
	snapshot := controller.Snapshot()
	for i := range naive.pumps {
		np := &naive.pumps[i]
		got := snapshot.Pumps[i]
		if got.State != np.state || got.Running != np.running || got.CumulativeTime != naive.total(np, naive.now) {
			t.Fatalf("pump %d mismatch got=%+v naive=%+v cumulative=%d", i+1, got, np, naive.total(np, naive.now))
		}
	}
	if snapshot.Target != naive.target || snapshot.DryLock != naive.dryLock {
		t.Fatalf("controller mismatch snapshot=%+v naiveTarget=%d dry=%v", snapshot, naive.target, naive.dryLock)
	}
}
