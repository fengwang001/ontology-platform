package ocstate

import (
	"errors"
	"testing"
)

func TestReportSeqStrictlyIncreasing(t *testing.T) {
	s := NewServer(1)
	if err := s.Report(5, 40, 1000, 0); err != nil {
		t.Fatalf("首个通告: %v", err)
	}
	if err := s.Report(5, 40, 1000, 0); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("seq 相等应报通告过期, got %v", err)
	}
	if err := s.Report(4, 40, 1000, 0); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("seq 更小应报通告过期, got %v", err)
	}
	if err := s.Report(6, 40, 1000, 0); err != nil {
		t.Fatalf("seq 更大应接受: %v", err)
	}
	if s.MaxSeq != 6 {
		t.Fatalf("MaxSeq = %d, want 6", s.MaxSeq)
	}
}

func TestRevokeClearsDButZeroPercentKeepsD(t *testing.T) {
	// validity=0 撤销：p 置 0、D 清零。
	s := NewServer(1)
	mustReport(t, s, 1, 50, 10_000, 0)
	s.D = 300
	mustReport(t, s, 2, 50, 0, 10)
	if s.EffectiveP(10) != 0 || s.EffectiveD(10) != 0 {
		t.Fatalf("撤销后 p=%d D=%d, 应都为 0", s.EffectiveP(10), s.EffectiveD(10))
	}

	// p=0 但 validity>0：D 保留不清。
	s2 := NewServer(2)
	mustReport(t, s2, 1, 50, 10_000, 0)
	s2.D = 300
	mustReport(t, s2, 2, 0, 10_000, 10)
	if got := s2.EffectiveD(20); got != 300 {
		t.Fatalf("p=0 通告后 D=%d, 应保留 300", got)
	}
	if got := s2.EffectiveP(20); got != 0 {
		t.Fatalf("p=0 通告后 p=%d, 应为 0", got)
	}
	t.Logf("判定依据: validity=0 撤销清零; p=0 且 validity>0 只停增不清欠额")
}

func TestExpiryIsPureFunctionOfTime(t *testing.T) {
	s := NewServer(1)
	mustReport(t, s, 1, 40, 1000, 0) // e = 1000
	s.D = 250
	if got := s.EffectiveP(999); got != 40 {
		t.Fatalf("t=999 p=%d, want 40", got)
	}
	if got := s.EffectiveD(999); got != 250 {
		t.Fatalf("t=999 D=%d, want 250", got)
	}
	if got := s.EffectiveP(1000); got != 0 {
		t.Fatalf("t=1000 恰到期 p=%d, want 0", got)
	}
	if got := s.EffectiveD(1000); got != 0 {
		t.Fatalf("t=1000 恰到期 D=%d, want 0", got)
	}
	// 到期后新通告从 D=0 起算。
	mustReport(t, s, 2, 30, 500, 1000)
	if got := s.EffectiveD(1000); got != 0 {
		t.Fatalf("到期后新通告 D=%d, want 0", got)
	}
	if got := s.EffectiveP(1000); got != 30 {
		t.Fatalf("新通告 p=%d, want 30", got)
	}
	// 未到期时换通告 D 保留。
	s.D = 120
	mustReport(t, s, 3, 60, 500, 1100)
	if got := s.EffectiveD(1100); got != 120 {
		t.Fatalf("未到期换通告 D=%d, 应保留 120", got)
	}
}

func TestDone(t *testing.T) {
	s := NewServer(1)
	if err := s.Done(); !errors.Is(err, ErrNoInFlight) {
		t.Fatalf("无在途应报 ErrNoInFlight, got %v", err)
	}
	s.InFlight = 2
	if err := s.Done(); err != nil {
		t.Fatal(err)
	}
	if err := s.Done(); err != nil {
		t.Fatal(err)
	}
	if err := s.Done(); !errors.Is(err, ErrNoInFlight) {
		t.Fatalf("减到 0 后再 Done 应报 ErrNoInFlight, got %v", err)
	}
}

func mustReport(t *testing.T, s *Server, seq, percent, validity, now int64) {
	t.Helper()
	if err := s.Report(seq, percent, validity, now); err != nil {
		t.Fatalf("Report(seq=%d): %v", seq, err)
	}
}
