package fedalloc

import (
	"fmt"
	"strings"
	"testing"
)

// opLogger records every operation's input, actual output and verdict basis so
// that test runs are reproducible and inspectable with `go test -v`.
type opLogger struct {
	t    *testing.T
	sb   strings.Builder
	step int
}

func newLogger(t *testing.T) *opLogger {
	t.Helper()
	l := &opLogger{t: t}
	l.log("== case %s ==", t.Name())
	return l
}

func (l *opLogger) log(format string, args ...any) {
	l.step++
	fmt.Fprintf(&l.sb, "[%03d] %s\n", l.step, fmt.Sprintf(format, args...))
}

func (l *opLogger) flush() { l.t.Log("\n" + l.sb.String()) }

func (l *opLogger) check(cond bool, basis string, args ...any) {
	l.t.Helper()
	msg := fmt.Sprintf(basis, args...)
	if cond {
		l.log("PASS: %s", msg)
		return
	}
	l.log("FAIL: %s", msg)
	l.flush()
	l.t.Fatalf("assertion failed: %s", msg)
}

func (l *opLogger) upsert(r *Registry, c Cluster) {
	l.log("UPSERT input=%+v", c)
	err := r.Upsert(c)
	l.log("UPSERT output err=%v", err)
	l.check(err == nil, "upsert accepted: %v", err)
}

func (l *opLogger) expectUpsertError(r *Registry, c Cluster, kind ErrorKind) {
	l.log("UPSERT(input=%+v) expect kind=%d", c, kind)
	err := r.Upsert(c)
	l.log("UPSERT output err=%v", err)
	l.check(err != nil && err.Kind() == kind, "rejected with expected kind %d (got %v)", kind, err)
}

func (l *opLogger) alloc(r *Registry, total int64) AllocationResult {
	l.log("ALLOCATE input total=%d", total)
	res, err := r.Allocate(total)
	l.log("ALLOCATE output targets=%v plan=%+v migration=%d err=%v",
		res.Targets, res.Plan, res.TotalMigration, err)
	l.check(err == nil, "allocation accepted: %v", err)
	return res
}

func (l *opLogger) expectAllocError(r *Registry, total int64, kind ErrorKind) *AllocationError {
	l.log("ALLOCATE(input total=%d) expect kind=%d", total, kind)
	res, err := r.Allocate(total)
	l.log("ALLOCATE output res=%+v err=%v", res, err)
	l.check(err != nil && err.Kind() == kind, "rejected with expected kind %d (got %v)", kind, err)
	return err
}

func targetsMap(res AllocationResult) map[string]int64 {
	m := map[string]int64{}
	for _, tg := range res.Targets {
		m[tg.Name] = tg.Replicas
	}
	return m
}

func ptr64(v int64) *int64 { return &v }

// TestRemainderTieBreak: equal fractional parts; higher current load wins the
// +1 bonus; equal loads fall back to name ascending.
func TestRemainderTieBreak(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	// remainder 1 over three weight-1 clusters => fractional parts tie.
	l.upsert(r, Cluster{Name: "x", Weight: 1, Capacity: 100, Available: true, CurrentReplicas: 1})
	l.upsert(r, Cluster{Name: "y", Weight: 1, Capacity: 100, Available: true, CurrentReplicas: 9})
	l.upsert(r, Cluster{Name: "z", Weight: 1, Capacity: 100, Available: true, CurrentReplicas: 9})
	res := l.alloc(r, 4)
	m := targetsMap(res)
	// y and z (load 9) beat x (load 1); y < z by name => the bonus goes to y.
	l.check(m["x"] == 1 && m["y"] == 2 && m["z"] == 1,
		"bonus goes to y (fraction tie, load 9, name<z): %v", m)
}

// TestSaturationRedistribution: capped clusters drop out and overflow is
// re-shared by remaining weight in subsequent rounds.
func TestSaturationRedistribution(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "a", Weight: 1, Capacity: 100, Available: true})
	l.upsert(r, Cluster{Name: "b", Weight: 1, Capacity: 5, Available: true})
	l.upsert(r, Cluster{Name: "c", Weight: 2, Capacity: 6, Available: true})
	// total 20, W=4: ideal a5 b5 c10; c pinned to 6, b already 5, overflow 4
	// goes to a in the next round => a=9 b=5 c=6.
	res := l.alloc(r, 20)
	m := targetsMap(res)
	l.check(m["a"] == 9 && m["b"] == 5 && m["c"] == 6,
		"multi-round saturation yields a9 b5 c6: %v", m)
	l.check(res.TotalMigration == 0, "no current load => no migration")
}

func TestZeroWeightCluster(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "z0", Weight: 0, MinReplicas: 4, Capacity: 9, Available: true})
	l.upsert(r, Cluster{Name: "w1", Weight: 3, Capacity: 100, Available: true})
	res := l.alloc(r, 10)
	m := targetsMap(res)
	l.check(m["z0"] == 4 && m["w1"] == 6, "zero-weight cluster gets only minimum: %v", m)
}

func TestUnavailableEvacuation(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "down", Weight: 5, Capacity: 100, CurrentReplicas: 7, Available: false})
	l.upsert(r, Cluster{Name: "up1", Weight: 1, Capacity: 100, CurrentReplicas: 3, Available: true})
	l.upsert(r, Cluster{Name: "up2", Weight: 1, Capacity: 100, CurrentReplicas: 3, Available: true})
	res := l.alloc(r, 10)
	m := targetsMap(res)
	l.check(m["down"] == 0 && m["up1"] == 5 && m["up2"] == 5,
		"unavailable cluster target zero, load evacuated: %v", m)
	l.check(res.TotalMigration == 7, "migration equals evacuated 7: got %d", res.TotalMigration)
	names := map[string]bool{}
	for _, ch := range res.Plan {
		names[ch.Name] = true
	}
	l.check(names["down"], "down appears in change plan")
}

func TestConfigConflict(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "a", Weight: 1, MinReplicas: 10, Capacity: 4, Available: true})
	l.expectAllocError(r, 50, KindConfigConflict)
	snap := r.Snapshot()
	l.check(len(snap) == 1 && snap[0].CurrentReplicas == 0, "rejected request changes no state")
}

func TestMinExceedsTotal(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "a", Weight: 1, MinReplicas: 6, Capacity: 100, Available: true})
	l.upsert(r, Cluster{Name: "b", Weight: 1, MinReplicas: 6, Capacity: 100, Available: true})
	l.expectAllocError(r, 10, KindMinExceedsTotal)
}

func TestInsufficientCapacityShortfall(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "a", Weight: 1, Capacity: 3, Available: true})
	l.upsert(r, Cluster{Name: "b", Weight: 2, Capacity: 4, Available: true})
	err := l.expectAllocError(r, 20, KindInsufficientCapacity)
	l.check(strings.Contains(err.Error(), "13"), "shortfall reported as 13: %v", err)
}

func TestHugeNumbers(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	const big int64 = 1_000_000_000_000_000 // 10^15
	l.upsert(r, Cluster{Name: "p", Weight: big, Capacity: 10 * big, MinReplicas: big / 2,
		Available: true})
	l.upsert(r, Cluster{Name: "q", Weight: big, Capacity: 10 * big, MinReplicas: big / 2,
		Available: true})
	res := l.alloc(r, 10*big)
	m := targetsMap(res)
	l.check(m["p"]+m["q"] == 10*big && m["p"] == 5*big && m["q"] == 5*big,
		"10^15-scale exact split: p=%d q=%d", m["p"], m["q"])
}
