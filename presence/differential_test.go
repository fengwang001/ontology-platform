package presence

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// opKind 是随机序列中的操作类型。
type opKind int

const (
	opReport opKind = iota
	opOffline
	opSetInvisible
	opBlock
	opUnblock
	opSubscribe
	opQuery
	opDrain
	opKindCount
)

type randOp struct {
	kind   opKind
	a, b   string
	status Status
	now    int64
	on     bool
}

func TestDifferentialAgainstNaive(t *testing.T) {
	logFile, err := os.Create("differential_run.log")
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer logFile.Close()
	log := bufio.NewWriter(logFile)
	defer log.Flush()

	const runs = 1500
	for run := 0; run < runs; run++ {
		seed := int64(1000 + run)
		rng := rand.New(rand.NewSource(seed))
		lease := int64(1 + rng.Intn(30)) // 小租约，制造大量到期
		svc, err := New(lease)
		if err != nil {
			t.Fatal(err)
		}
		nm := newNaive(lease)

		users := []string{"u0", "u1", "u2", "u3", "u4"}
		viewers := append(append([]string{}, users...), []string{"v0", "v1"}...)
		devices := []string{"d0", "d1", "d2", "d3", "d4", "d5", "d6", "d7", "d8"}
		statuses := []Status{StatusOnline, StatusBusy, StatusAway}

		var now int64
		fmt.Fprintf(log, "=== RUN %d seed=%d lease=%d ===\n", run, seed, lease)

		steps := 30 + rng.Intn(90)
		for step := 0; step < steps; step++ {
			// now 以大概率前进、小概率持平、极小概率回退（验证拒绝）。
			switch rng.Intn(10) {
			case 0:
			case 1:
				if now > 0 {
					now--
				}
			default:
				now += int64(rng.Intn(int(lease) + 4))
			}

			op := randOp{kind: opKind(rng.Intn(int(opKindCount))), now: now}
			op.a = users[rng.Intn(len(users))]
			switch op.kind {
			case opReport, opOffline:
				op.b = devices[rng.Intn(len(devices))]
				op.status = statuses[rng.Intn(len(statuses))]
			case opSetInvisible:
				op.on = rng.Intn(2) == 0
			case opBlock, opUnblock:
				op.b = viewers[rng.Intn(len(viewers))]
			case opSubscribe, opQuery:
				op.b = users[rng.Intn(len(users))]
			case opDrain:
				op.a = viewers[rng.Intn(len(viewers))]
			}

			got, gotErr := runOnService(svc, op)
			want, wantErr := runOnNaive(nm, op)

			fmt.Fprintf(log, "step=%d %s now=%d a=%q b=%q st=%s on=%v -> got=(%s,%v) want=(%s,%v)\n",
				step, opName(op.kind), op.now, op.a, op.b, op.status, op.on,
				got, errText(gotErr), want, errText(wantErr))

			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("run=%d seed=%d step=%d %s: error mismatch got=%v want=%v",
					run, seed, step, opName(op.kind), gotErr, wantErr)
			}
			if gotErr == nil && got != want {
				t.Fatalf("run=%d seed=%d step=%d %s:\n got=%s\nwant=%s\n(log: differential_run.log)",
					run, seed, step, opName(op.kind), got, want)
			}
		}
	}
}

func runOnService(s *Service, op randOp) (string, error) {
	switch op.kind {
	case opReport:
		return "ok", s.Report(op.a, op.b, op.status, op.now)
	case opOffline:
		return "ok", s.Offline(op.a, op.b, op.now)
	case opSetInvisible:
		return "ok", s.SetInvisible(op.a, op.on, op.now)
	case opBlock:
		return "ok", s.Block(op.a, op.b, op.now)
	case opUnblock:
		return "ok", s.Unblock(op.a, op.b, op.now)
	case opSubscribe:
		return "ok", s.Subscribe(op.a, op.b, op.now)
	case opQuery:
		q, err := s.Query(op.a, op.b, op.now)
		return fmt.Sprintf("status=%s active=%d devs=%v", q.Status, q.ActiveCount, q.Devices), err
	case opDrain:
		d, err := s.Drain(op.a, op.now)
		return fmt.Sprintf("dropped=%d notifs=%v", d.Dropped, d.Notifications), err
	}
	return "", nil
}

func runOnNaive(m *naiveModel, op randOp) (string, error) {
	switch op.kind {
	case opReport:
		return "ok", m.Report(op.a, op.b, op.status, op.now)
	case opOffline:
		return "ok", m.Offline(op.a, op.b, op.now)
	case opSetInvisible:
		return "ok", m.SetInvisible(op.a, op.on, op.now)
	case opBlock:
		return "ok", m.Block(op.a, op.b, op.now)
	case opUnblock:
		return "ok", m.Unblock(op.a, op.b, op.now)
	case opSubscribe:
		return "ok", m.Subscribe(op.a, op.b, op.now)
	case opQuery:
		q, err := m.Query(op.a, op.b, op.now)
		return fmt.Sprintf("status=%s active=%d devs=%v", q.Status, q.ActiveCount, q.Devices), err
	case opDrain:
		d, err := m.Drain(op.a, op.now)
		return fmt.Sprintf("dropped=%d notifs=%v", d.Dropped, d.Notifications), err
	}
	return "", nil
}

func opName(k opKind) string {
	return [...]string{"Report", "Offline", "SetInvisible", "Block", "Unblock", "Subscribe", "Query", "Drain"}[k]
}

func errText(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
