package ontology

import (
	"math/rand"
	"strings"
	"testing"
)

func randomConfig(r *rand.Rand) Config {
	minRuns := 2 + r.Intn(4)
	maxRuns := minRuns + r.Intn(5)
	minMerge := 2 + r.Intn(3)
	maxMerge := minMerge + r.Intn(4)
	cfg := Config{
		MinRuns:  minRuns,
		MaxRuns:  maxRuns,
		A:        1 + r.Int63n(1000),
		Rho:      r.Int63n(50),
		MinMerge: minMerge,
		MaxMerge: maxMerge,
		Cmax:     1 + r.Intn(3),
		P:        0,
	}
	if r.Intn(3) == 0 {
		cfg.P = int64(1 + r.Intn(20))
	}
	return cfg
}

func generateOps(r *rand.Rand) []op {
	var ops []op
	now := int64(r.Intn(3))
	n := 40 + r.Intn(120)
	for len(ops) < n {
		switch r.Intn(10) {
		case 0, 1, 2, 3:
			size := int64(1 + r.Intn(60))
			if r.Intn(12) == 0 {
				size = int64(1 + r.Intn(1_000_000))
			}
			ops = append(ops, op{kind: opAdd, now: now, size: size})
		case 4, 5, 6:
			ops = append(ops, op{kind: opPick, now: now})
		case 7, 8:
			ops = append(ops, op{kind: opDone, now: now,
				pid:  1 + r.Int63n(6),
				size: int64(1 + r.Intn(60))})
		default:
			ops = append(ops, op{kind: opAbort, pid: 1 + r.Int63n(6)})
		}
		if r.Intn(15) == 0 {
			now--
			if now < 0 {
				now = 0
			}
		}
		now += int64(r.Intn(3))
	}
	return ops
}

func maybeBad(inject bool, i, total int) bool {
	return inject && (i == total/2 || i == total/2+1)
}

func replayReal(t *testing.T, cfg Config, ops []op, injectBad bool) []step {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var out []step
	for i, o := range ops {
		switch o.kind {
		case opAdd:
			size := o.size
			if maybeBad(injectBad, i, len(ops)) {
				size = 0
			}
			id, err := c.AddRun(o.now, size)
			out = append(out, step{kind: "Add", err: errCode(err), id: id,
				runs: c.snapshotRuns()})
		case opPick:
			p, err := c.Pick(o.now)
			if err == nil {
				out = append(out, step{kind: "Pick", err: "nil", id: p.ID,
					reason: p.Reason, sel: append([]int64(nil), p.Runs...),
					total: p.Total, probes: c.probes, runs: c.snapshotRuns()})
			} else {
				out = append(out, step{kind: "Pick", err: errCode(err),
					probes: c.probes, runs: c.snapshotRuns()})
			}
		case opDone:
			size := o.size
			if maybeBad(injectBad, i, len(ops)) {
				size = 0
			}
			nr, err := c.Done(o.now, o.pid, size)
			st := step{kind: "Done", err: errCode(err), runs: c.snapshotRuns()}
			if err == nil {
				st.id = nr.ID
			}
			out = append(out, st)
		case opAbort:
			err := c.Abort(o.pid)
			out = append(out, step{kind: "Abort", err: errCode(err),
				runs: c.snapshotRuns()})
		}
	}
	return out
}

func replayNaive(cfg Config, ops []op, injectBad bool) ([]step, *naiveSim) {
	s := newNaiveSim(cfg)
	var out []step
	for i, o := range ops {
		switch o.kind {
		case opAdd:
			size := o.size
			if maybeBad(injectBad, i, len(ops)) {
				size = 0
			}
			id, err := s.addRun(o.now, size)
			out = append(out, step{kind: "Add", err: errCode(err), id: id,
				runs: s.snapshotRuns()})
		case opPick:
			p, err := s.pick(o.now)
			if err == nil {
				out = append(out, step{kind: "Pick", err: "nil", id: p.id,
					reason: p.reason, sel: append([]int64(nil), p.runs...),
					total: p.total, probes: s.probes,
					runs: s.snapshotRuns(), basis: p.basis})
			} else {
				out = append(out, step{kind: "Pick", err: errCode(err),
					probes: s.probes, runs: s.snapshotRuns()})
			}
		case opDone:
			size := o.size
			if maybeBad(injectBad, i, len(ops)) {
				size = 0
			}
			nr, err := s.done(o.now, o.pid, size)
			st := step{kind: "Done", err: errCode(err), runs: s.snapshotRuns()}
			if err == nil {
				st.id = nr.id
			}
			out = append(out, st)
		case opAbort:
			err := s.abort(o.pid)
			out = append(out, step{kind: "Abort", err: errCode(err),
				runs: s.snapshotRuns()})
		}
	}
	return out, s
}

func TestRandomDifferential2000(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		seed := int64(1 + seq*7919)
		r := rand.New(rand.NewSource(seed))
		cfg := randomConfig(r)
		ops := generateOps(r)
		injectBad := seq%5 == 0

		real := replayReal(t, cfg, ops, injectBad)
		naive, sim := replayNaive(cfg, ops, injectBad)
		replayed, _ := replayReal(t, cfg, ops, injectBad), (*naiveSim)(nil)

		if len(real) != len(naive) {
			t.Fatalf("seq %d length mismatch %d vs %d", seq, len(real), len(naive))
		}
		for i := range real {
			if !sameStep(real[i], naive[i]) {
				var sb strings.Builder
				sb.WriteString(dumpContext(cfg, ops, i, real, naive))
				t.Fatalf("seq %d seed %d mismatch at op %d:\nreal:  %s\nnaive: %s\n\n%s",
					seq, seed, i, real[i], naive[i], sb.String())
			}
			if !sameStep(real[i], replayed[i]) {
				t.Fatalf("seq %d nondeterministic replay at op %d:\n%s\nvs\n%s",
					seq, i, real[i], replayed[i])
			}
		}

		// Probe bound: n * MaxMerge across the whole sequence.
		for i := range real {
			if ops[i].kind == opPick &&
				(real[i].err == "nil" || real[i].err == "ErrNotNeeded") &&
				real[i].probes > len(real[i].runs)*cfg.MaxMerge {
				t.Fatalf("seq %d probes %d > n*MaxMerge at op %d",
					seq, real[i].probes, i)
			}
		}

		if seq < 3 || seq%400 == 0 {
			t.Logf("seq=%d cfg=%+v\n%s", seq, cfg, sim.logText())
		}
	}
}

func dumpContext(cfg Config, ops []op, idx int, real, naive []step) string {
	var sb strings.Builder
	sb.WriteString("cfg: ")
	sb.WriteString(stringifyCfg(cfg))
	sb.WriteString("\nlast ops/results:\n")
	start := idx - 8
	if start < 0 {
		start = 0
	}
	for i := start; i <= idx && i < len(ops); i++ {
		o := ops[i]
		sb.WriteString(opLine(o))
		sb.WriteString("   real:  ")
		sb.WriteString(real[i].String())
		sb.WriteString("\n   naive: ")
		sb.WriteString(naive[i].String())
		sb.WriteString("\n")
	}
	return sb.String()
}

func opLine(o op) string {
	switch o.kind {
	case opAdd:
		return "Add(now=" + itoa(o.now) + ",size=" + itoa(o.size) + ")"
	case opPick:
		return "Pick(now=" + itoa(o.now) + ")"
	case opDone:
		return "Done(now=" + itoa(o.now) + ",plan=" + itoa(o.pid) + ",out=" + itoa(o.size) + ")"
	default:
		return "Abort(plan=" + itoa(o.pid) + ")"
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [24]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func stringifyCfg(cfg Config) string {
	return "{MinRuns:" + itoa(int64(cfg.MinRuns)) +
		" MaxRuns:" + itoa(int64(cfg.MaxRuns)) +
		" A:" + itoa(cfg.A) + " Rho:" + itoa(cfg.Rho) +
		" MinMerge:" + itoa(int64(cfg.MinMerge)) +
		" MaxMerge:" + itoa(int64(cfg.MaxMerge)) +
		" Cmax:" + itoa(int64(cfg.Cmax)) + " P:" + itoa(cfg.P) + "}"
}
