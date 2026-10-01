package nodeadmission

import (
	"fmt"
	"testing"
)

// Rejection reason precedence and "rejected op changes nothing".
func TestRejectOrderingAndNoMutation(t *testing.T) {
	m, _ := NewManager(0, 10)
	_ = m.AddNode("n", 2)
	_ = m.Taint("n", Taint{"k", "v", NoSchedule}, 0)
	_ = m.Schedule("p", "n", []Toleration{neTol("k", OpEqual, "v", NoSchedule, -1)}, 0)

	// Invalid config beats everything at construction.
	if _, err := NewManager(-1, 0); errCode(err) != ErrInvalidConfig {
		t.Fatalf("bad G and R => ErrInvalidConfig, got %v", err)
	}
	if _, err := NewManager(0, 1_000_001); errCode(err) != ErrInvalidConfig {
		t.Fatalf("R out of range => ErrInvalidConfig, got %v", err)
	}

	// Invalid argument beats clock-moved-back.
	if err := m.Schedule("x", "n", nil, -1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("now<0 => ErrInvalidArgument, got %v", err)
	}
	// Invalid toleration shape: non-NoExecute toleration with seconds set.
	bad := []Toleration{neTol("k", OpEqual, "v", "", 5)}
	if err := m.Schedule("x", "n", bad, 1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("seconds on effect='' toleration => ErrInvalidArgument, got %v", err)
	}
	// Exists must carry empty value.
	bad = []Toleration{neTol("k", OpExists, "z", NoExecute, -1)}
	if err := m.Schedule("x", "n", bad, 1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("Exists with value => ErrInvalidArgument, got %v", err)
	}
	// Empty key requires Exists.
	bad = []Toleration{neTol("", OpEqual, "", NoExecute, -1)}
	if err := m.Schedule("x", "n", bad, 1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("empty key with Equal => ErrInvalidArgument, got %v", err)
	}
	// Taint validation: empty key, bad effect.
	if err := m.Taint("n", Taint{"", "v", NoExecute}, 1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("empty taint key => ErrInvalidArgument, got %v", err)
	}
	if err := m.Taint("n", Taint{"k", "v", "Bogus"}, 1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("bad effect => ErrInvalidArgument, got %v", err)
	}

	// Clock moved back, with otherwise valid payload.
	if err := m.Taint("n", Taint{"k2", "v", NoExecute}, -5); errCode(err) != ErrInvalidArgument {
		t.Fatalf("invalid now precedes clock-back, got %v", err)
	}
	if _, err := m.Tick(-1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("Tick invalid now => ErrInvalidArgument, got %v", err)
	}
	if _, err := m.Tick(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Tick(0); errCode(err) != ErrClockMovedBack {
		t.Fatalf("now<lastNow => ErrClockMovedBack, got %v", err)
	}
	if err := m.Schedule("p", "n", nil, 0); errCode(err) != ErrClockMovedBack {
		t.Fatalf("clock-back precedes pod-exists, got %v", err)
	}

	// Pod-exists precedes node-not-found.
	if err := m.Schedule("p", "ghost", nil, 2); errCode(err) != ErrPodExists {
		t.Fatalf("pod-exists precedes node-not-found, got %v", err)
	}
	// Node-not-found precedes untolerated, which precedes capacity.
	if err := m.Schedule("q", "ghost", nil, 2); errCode(err) != ErrNodeNotFound {
		t.Fatalf("missing node => ErrNodeNotFound, got %v", err)
	}

	// Untolerated precedes capacity: fill node and keep taint.
	_ = m.Schedule("p2", "n", []Toleration{neTol("k", OpEqual, "v", NoSchedule, -1)}, 2)
	if err := m.Schedule("p3", "n", nil, 3); errCode(err) != ErrUntoleratedTaint {
		t.Fatalf("untolerated taint precedes capacity, got %v", err)
	}
	if err := m.Schedule("p3", "n", []Toleration{neTol("k", OpEqual, "v", NoSchedule, -1)}, 3); errCode(err) != ErrCapacity {
		t.Fatalf("tolerated but full node => ErrCapacity, got %v", err)
	}

	// First untolerated taint ordering: by key bytes then effect bytes.
	m2, _ := NewManager(0, 10)
	_ = m2.AddNode("n", 10)
	_ = m2.Taint("n", Taint{"z", "v", NoExecute}, 0)
	_ = m2.Taint("n", Taint{"a", "v", NoExecute}, 0)
	_ = m2.Taint("n", Taint{"a", "v", NoSchedule}, 0)
	err := m2.Schedule("p", "n", nil, 0)
	re := err.(*RejectError)
	if errCode(err) != ErrUntoleratedTaint || re.Detail != "untolerated taint: a/NoExecute" {
		// effect bytes: NoExecute < NoSchedule ('E' < 'S')
		t.Fatalf("first untolerated taint must be a/NoExecute, got %v", err)
	}

	// AddNode: invalid argument precedes duplicate; duplicate distinct code.
	if err := m.AddNode("", 1); errCode(err) != ErrInvalidArgument {
		t.Fatalf("empty node name => ErrInvalidArgument, got %v", err)
	}
	if err := m.AddNode("n", 0); errCode(err) != ErrInvalidArgument {
		t.Fatalf("maxPods out of range => ErrInvalidArgument, got %v", err)
	}
	if err := m.AddNode("n", 1); errCode(err) != ErrNodeExists {
		t.Fatalf("duplicate node => ErrNodeExists, got %v", err)
	}

	// Untaint: node-not-found precedes taint-not-found; taint missing distinct.
	if err := m.Untaint("ghost", "k", NoExecute, 5); errCode(err) != ErrNodeNotFound {
		t.Fatalf("missing node on Untaint => ErrNodeNotFound, got %v", err)
	}
	if err := m.Untaint("n", "missing", NoExecute, 5); errCode(err) != ErrTaintNotFound {
		t.Fatalf("missing taint => ErrTaintNotFound, got %v", err)
	}

	// None of the rejected operations changed state: node n still has exactly
	// one taint k/NoSchedule and two pods p,p2; rejected AddNode/ticks no effect.
	if err := m.Schedule("p3", "n", []Toleration{neTol("k", OpEqual, "v", NoSchedule, -1)}, 6); errCode(err) != ErrCapacity {
		t.Fatalf("state must be unchanged after rejections (still full), got %v", err)
	}
}

// Concurrent calls must be safe; capacity/tick invariants must always hold.
func TestConcurrentSafety(t *testing.T) {
	m, _ := NewManager(5, 2)
	_ = m.AddNode("n", 3)
	_ = m.AddNode("m", 3)
	_ = m.Taint("n", Taint{"k", "v", NoExecute}, 0)

	done := make(chan struct{})
	for w := 0; w < 8; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			tol := []Toleration{neTol("k", OpEqual, "v", "", -1)}
			for i := 0; i < 200; i++ {
				now := int64(w*1000 + i)
				_ = m.Schedule(fmt.Sprintf("w%d-p%d", w, i), "n", tol, now)
				_, _ = m.Tick(now)
				_ = m.Taint("n", Taint{"k", "v", NoExecute}, now)
				_ = m.Untaint("n", "k", NoExecute, now)
				_ = m.Taint("n", Taint{"k", "v", NoExecute}, now)
			}
		}(w)
	}
	for w := 0; w < 8; w++ {
		<-done
	}
	// After all workers finish, no node exceeds its capacity.
	for name, ns := range m.nodes {
		if int64(len(ns.pods)) > ns.maxPods {
			t.Fatalf("node %s holds %d > maxPods %d", name, len(ns.pods), ns.maxPods)
		}
	}
}
