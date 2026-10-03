package erase

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type naiveLedger struct {
	systems    int
	sla        int
	clock      int
	held       map[int]bool
	subjects   []int
	status     []Status
	deadlines  []int
	acks       []map[int]int
	pendingAck []map[int]bool
	open       map[int]int
	backups    []int
	backupAt   []int
	restoring  []bool
	pending    []map[int]bool
}

type simulatedOp struct {
	name string
	args []int
}

type simulatedResult struct {
	id     int
	list   []int
	err    bool
	readOK bool
}

func newNaive(systems, sla int) *naiveLedger {
	return &naiveLedger{
		systems:   systems,
		sla:       sla,
		held:      make(map[int]bool),
		open:      make(map[int]int),
		restoring: make([]bool, systems+1),
		pending:   make([]map[int]bool, systems+1),
	}
}

func TestRandomOperationsAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iteration := 0; iteration < 2000; iteration++ {
		systems := 1 + rng.Intn(4)
		sla := 1 + rng.Intn(20)
		actual := New(systems, sla)
		model := newNaive(systems, sla)
		ops := make([]simulatedOp, 0, 80)

		for step := 0; step < 80; step++ {
			now := model.clock + rng.Intn(8)
			subject := 1 + rng.Intn(12)
			system := 1 + rng.Intn(systems)
			op := simulatedOp{args: []int{now, subject, system}}
			switch rng.Intn(11) {
			case 0:
				op.name = "request"
				op.args = []int{1, subject, now}
			case 1:
				op.name = "hold"
				op.args = []int{2, subject, now}
			case 2:
				op.name = "release"
				op.args = []int{2, subject, now}
			case 3:
				op.name = "ack"
				op.args = []int{3, 1 + rng.Intn(len(model.subjects)+2), system, now}
			case 4:
				op.name = "overdue"
				op.args = []int{now}
			case 5:
				op.name = "backup"
				op.args = []int{3, system, now}
			case 6:
				op.name = "restore"
				op.args = []int{3, system, rng.Intn(len(model.backups)+3) + 1, now}
			case 7:
				op.name = "reapply"
				op.args = []int{3, system, 1 + rng.Intn(len(model.subjects)+2), now}
			case 8:
				op.name = "read"
				op.args = []int{system}
			case 9:
				op.name = "bad-role"
				op.args = []int{2, subject, now}
			case 10:
				op.name = "clock-back"
				op.args = []int{1, subject, model.clock - 1}
			}
			if op.name == "clock-back" && model.clock == 0 {
				op = simulatedOp{name: "request", args: []int{1, subject, now}}
			}
			ops = append(ops, op)

			actualResult := runActual(t, actual, op)
			modelResult := model.run(op)
			if !sameResult(actualResult, modelResult) {
				t.Logf("debug model restoring=%v pending=%v", model.restoring, model.pending)
				t.Fatalf("iteration=%d step=%d input=%v actual=%+v model=%+v", iteration, step, op, actualResult, modelResult)
			}
		}

		model.assertMatches(t, actual, iteration)
		t.Logf("iteration=%d steps=80 basis=naive_state_and_outputs_matched final_statuses=%v final_restoring=%v",
			iteration, model.status, model.restoring[1:])
	}
}

func runActual(t *testing.T, l *Ledger, op simulatedOp) simulatedResult {
	t.Helper()
	switch op.name {
	case "request":
		id, err := l.Request(op.args[0], op.args[1], op.args[2])
		return simulatedResult{id: id, err: err != nil}
	case "hold":
		return simulatedResult{err: l.Hold(op.args[0], op.args[1], op.args[2]) != nil}
	case "release":
		return simulatedResult{err: l.Release(op.args[0], op.args[1], op.args[2]) != nil}
	case "ack":
		return simulatedResult{err: l.Ack(op.args[0], op.args[1], op.args[2], op.args[3]) != nil}
	case "overdue":
		entries := l.Overdue(op.args[0])
		result := simulatedResult{}
		for _, entry := range entries {
			result.list = append(result.list, entry.Erasure)
		}
		return result
	case "backup":
		id, err := l.Backup(op.args[0], op.args[1], op.args[2])
		return simulatedResult{id: id, err: err != nil}
	case "restore":
		list, err := l.Restore(op.args[0], op.args[1], op.args[2], op.args[3])
		return simulatedResult{list: append([]int(nil), list...), err: err != nil}
	case "reapply":
		return simulatedResult{err: l.ReapplyDone(op.args[0], op.args[2], op.args[1], op.args[3]) != nil}
	case "read":
		err := l.Read(op.args[0])
		return simulatedResult{readOK: err == nil, err: err != nil && !isRestoring(err)}
	case "bad-role":
		id, err := l.Request(op.args[0], op.args[1], op.args[2])
		return simulatedResult{id: id, err: err != nil}
	case "clock-back":
		id, err := l.Request(op.args[0], op.args[1], op.args[2])
		return simulatedResult{id: id, err: err != nil}
	default:
		t.Fatalf("unknown op %s", op.name)
		return simulatedResult{}
	}
}

func (m *naiveLedger) run(op simulatedOp) simulatedResult {
	args := op.args
	if op.name == "read" {
		invalid := args[0] < 1 || args[0] > m.systems
		return simulatedResult{readOK: !invalid && !m.restoring[args[0]], err: invalid}
	}
	if op.name == "overdue" {
		now := args[0]
		list := make([]int, 0)
		for id, status := range m.status {
			if status == Active && m.deadlines[id] <= now {
				list = append(list, id+1)
			}
		}
		return simulatedResult{list: list}
	}

	role, now := args[0], args[len(args)-1]
	invalid := role < 1 || role > 3 || now < 0
	if op.name == "ack" || op.name == "reapply" {
		invalid = invalid || args[1] < 1 || args[1] > len(m.subjects) || args[2] < 1 || args[2] > m.systems
	}
	if op.name == "request" || op.name == "hold" || op.name == "release" || op.name == "bad-role" || op.name == "clock-back" {
		invalid = invalid || args[1] < 1 || args[1] > 1_000_000
	}
	if op.name == "backup" {
		invalid = invalid || args[1] < 1 || args[1] > m.systems
	}
	if op.name == "restore" {
		invalid = invalid || args[1] < 1 || args[1] > m.systems || args[2] < 1 || args[2] > len(m.backups)
	}
	if invalid {
		return simulatedResult{err: true}
	}

	requiredRole := 3
	if op.name == "request" || op.name == "bad-role" || op.name == "clock-back" {
		requiredRole = 1
	}
	if op.name == "hold" || op.name == "release" {
		requiredRole = 2
	}
	if role != requiredRole {
		return simulatedResult{err: true}
	}
	if now < m.clock {
		return simulatedResult{err: true}
	}

	switch op.name {
	case "request", "bad-role", "clock-back":
		subject := args[1]
		if _, ok := m.open[subject]; ok {
			return simulatedResult{err: true}
		}
		m.clock = now
		id := len(m.subjects) + 1
		m.subjects = append(m.subjects, subject)
		m.acks = append(m.acks, make(map[int]int))
		pendingAck := make(map[int]bool)
		for system := 1; system <= m.systems; system++ {
			pendingAck[system] = true
		}
		m.pendingAck = append(m.pendingAck, pendingAck)
		m.deadlines = append(m.deadlines, 0)
		if m.held[subject] {
			m.status = append(m.status, Deferred)
		} else {
			m.status = append(m.status, Active)
			m.deadlines[id-1] = now + m.sla
		}
		m.open[subject] = id
		return simulatedResult{id: id}
	case "hold":
		subject := args[1]
		if m.held[subject] {
			return simulatedResult{err: true}
		}
		m.clock = now
		m.held[subject] = true
		return simulatedResult{}
	case "release":
		subject := args[1]
		if !m.held[subject] {
			return simulatedResult{err: true}
		}
		m.clock = now
		delete(m.held, subject)
		if id, ok := m.open[subject]; ok && m.status[id-1] == Deferred {
			m.status[id-1] = Active
			m.deadlines[id-1] = now + m.sla
		}
		return simulatedResult{}
	case "ack":
		id, system := args[1], args[2]
		if m.status[id-1] != Active || !m.pendingAck[id-1][system] {
			return simulatedResult{err: true}
		}
		m.clock = now
		m.acks[id-1][system] = now
		delete(m.pendingAck[id-1], system)
		pendingSystems := len(m.pendingAck[id-1])
		if pendingSystems == 0 {
			m.status[id-1] = Done
			delete(m.open, m.subjects[id-1])
		}
		return simulatedResult{}
	case "backup":
		system := args[1]
		m.clock = now
		id := len(m.backups) + 1
		m.backups = append(m.backups, system)
		m.backupAt = append(m.backupAt, now)
		return simulatedResult{id: id}
	case "restore":
		system, backup := args[1], args[2]
		if m.backups[backup-1] != system {
			return simulatedResult{err: true}
		}
		if m.restoring[system] {
			return simulatedResult{err: true}
		}
		m.clock = now
		list := make([]int, 0)
		for id := range m.acks {
			if ackedAt, ok := m.acks[id][system]; ok && ackedAt > m.backupAt[backup-1] {
				list = append(list, id+1)
			}
		}
		sort.Ints(list)
		if len(list) > 0 {
			m.restoring[system] = true
			m.pending[system] = make(map[int]bool)
			for _, id := range list {
				m.pending[system][id] = true
			}
		}
		return simulatedResult{list: list}
	case "reapply":
		id, system := args[1], args[2]
		if !m.restoring[system] || !m.pending[system][id] {
			return simulatedResult{err: true}
		}
		m.clock = now
		delete(m.pending[system], id)
		if len(m.pending[system]) == 0 {
			m.restoring[system] = false
		}
		return simulatedResult{}
	}
	return simulatedResult{err: true}
}

func sameResult(a, b simulatedResult) bool {
	return a.id == b.id && a.err == b.err && a.readOK == b.readOK && equalInts(a.list, b.list)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isRestoring(err error) bool {
	return err == ErrRestoring
}

func (m *naiveLedger) assertMatches(t *testing.T, l *Ledger, iteration int) {
	t.Helper()
	for id := range m.status {
		got, ok := l.Erasure(id + 1)
		if !ok || got.Status != m.status[id] || got.Deadline != m.deadlines[id] {
			t.Fatalf("iteration=%d erasure=%d actual=%+v model_status=%v model_deadline=%d", iteration, id+1, got, m.status[id], m.deadlines[id])
		}
		for system := 1; system <= m.systems; system++ {
			modelAck, modelHasAck := m.acks[id][system]
			actualAck, actualHasAck := got.Acks[system]
			if modelHasAck != actualHasAck || modelAck != actualAck {
				t.Fatalf("iteration=%d erasure=%d system=%d acks=%v model=%d", iteration, id+1, system, got.Acks, m.acks[id][system])
			}
		}
	}
	for system := 1; system <= m.systems; system++ {
		err := l.Read(system)
		if m.restoring[system] && err != ErrRestoring {
			t.Fatalf("iteration=%d system=%d expected restoring", iteration, system)
		}
		if !m.restoring[system] && err != nil {
			t.Fatalf("iteration=%d system=%d read err=%v", iteration, system, err)
		}
	}
}

func TestNaiveComparisonProducesReadableLog(t *testing.T) {
	t.Logf("sample input=%s output=%s basis=%s",
		fmt.Sprint(simulatedOp{name: "request", args: []int{1, 7, 10}}),
		fmt.Sprint(simulatedResult{id: 1}),
		"privacy_officer_request_accepted_active")
}
