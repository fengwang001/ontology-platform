package filter

import (
	"testing"

	"ontology/profile"
)

func TestPendingAndHeartbeat(t *testing.T) {
	p := profile.Params{DB: 5, MinInterval: 100, MaxInterval: 1000, Low: 0, High: 100}
	s := NewState()

	if _, ok := s.PendingAt(p); ok {
		t.Fatal("no cur should have no pending")
	}
	if _, ok := s.HeartbeatAt(p); ok {
		t.Fatal("no last should have no heartbeat")
	}

	s.Ingest(0, 50, p, 2)
	if at, ok := s.PendingAt(p); !ok || at != 0 {
		t.Fatalf("first pending at=%d ok=%v want 0", at, ok)
	}
	s.Report(0, 50)

	// 差恰等 db：不越死区。
	s.Ingest(10, 55, p, 2)
	if _, ok := s.PendingAt(p); ok {
		t.Fatal("diff == db must not pending")
	}
	// 越过死区：dc=max(lastAt+minI, cur.ts, defer)。
	s.Ingest(20, 56, p, 2)
	if at, ok := s.PendingAt(p); !ok || at != 100 {
		t.Fatalf("pending dc got %d ok=%v want 100", at, ok)
	}
	if at, ok := s.HeartbeatAt(p); !ok || at != 1000 {
		t.Fatalf("heartbeat got %d ok=%v want 1000", at, ok)
	}

	// 推迟下限同时作用于待报与心跳。
	s.Throttle(1200)
	if at, _ := s.PendingAt(p); at != 1200 {
		t.Fatalf("pending defer got %d want 1200", at)
	}
	if at, _ := s.HeartbeatAt(p); at != 1200 {
		t.Fatalf("heartbeat defer got %d want 1200", at)
	}
	s.Report(1200, 56)
	if s.DeferUntil() != 0 {
		t.Fatalf("report must clear defer, got %d", s.DeferUntil())
	}
}

func TestFaultAndRecover(t *testing.T) {
	p := profile.Params{DB: 0, MinInterval: 100, MaxInterval: 1000, Low: 0, High: 100}
	s := NewState()
	s.Ingest(0, 50, p, 2)
	s.Report(0, 50)

	if _, f := s.Ingest(10, 200, p, 2); f {
		t.Fatal("first bad sample must not fault")
	}
	// 有效样本复位 bad。
	s.Ingest(20, 60, p, 2)
	if _, f := s.Ingest(30, 200, p, 2); f {
		t.Fatal("bad counter should reset on valid sample")
	}
	if _, f := s.Ingest(40, -1, p, 2); !f {
		t.Fatal("second consecutive bad after reset must fault")
	}

	p2 := p
	s2 := NewState()
	s2.Ingest(0, 50, p2, 2)
	s2.Report(0, 50)
	if _, f := s2.Ingest(1, 200, p2, 2); f {
		t.Fatal("not yet")
	}
	if _, f := s2.Ingest(2, -1, p2, 2); !f || !s2.Fault() {
		t.Fatal("second consecutive bad must fault")
	}
	if _, ok := s2.PendingAt(p2); ok {
		t.Fatal("fault suppresses events")
	}
	// 有效样本解除故障并复位基准。
	rec, f := s2.Ingest(3, 60, p2, 2)
	if !rec || f || s2.Fault() {
		t.Fatalf("recover wrong rec=%v faulted=%v", rec, f)
	}
	if la, ok := s2.LastAt(); !ok || la != 3 {
		t.Fatalf("recover must set lastAt=3, got %d ok=%v", la, ok)
	}
}
