package ocstate

import (
	"errors"
	"testing"
)

func mustAdd(t *testing.T, s *Store, id int) {
	t.Helper()
	if err := s.AddServer(id); err != nil {
		t.Fatalf("AddServer(%d): %v", id, err)
	}
}

func TestAddServer(t *testing.T) {
	s := NewStore()
	for _, id := range []int{0, -1, 1_000_001} {
		if err := s.AddServer(id); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("AddServer(%d) err=%v, want ErrInvalidParam", id, err)
		}
	}
	mustAdd(t, s, 1)
	mustAdd(t, s, 1_000_000)
	if err := s.AddServer(1); !errors.Is(err, ErrServerExists) {
		t.Fatalf("duplicate AddServer err=%v, want ErrServerExists", err)
	}
}

func TestReportParamAndClock(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, 7)
	cases := []struct {
		name                        string
		server                      int
		seq, percent, validity, now int64
		want                        error
	}{
		{"bad server id", 0, 1, 50, 100, 0, ErrInvalidParam},
		{"negative seq", 7, -1, 50, 100, 0, ErrInvalidParam},
		{"percent over 100", 7, 1, 101, 100, 0, ErrInvalidParam},
		{"negative percent", 7, 1, -1, 100, 0, ErrInvalidParam},
		{"validity too large", 7, 1, 50, MaxValidity + 1, 0, ErrInvalidParam},
		{"negative validity", 7, 1, 50, -1, 0, ErrInvalidParam},
		{"now too large", 7, 1, 50, 100, MaxNow + 1, ErrInvalidParam},
		{"negative now", 7, 1, 50, 100, -1, ErrInvalidParam},
		{"unknown server", 8, 1, 50, 100, 0, ErrServerNotExist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Report(tc.server, tc.seq, tc.percent, tc.validity, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Report err=%v, want %v", err, tc.want)
			}
		})
	}
	// 全部被拒后时钟未推进：now=0 仍应被接受。
	if err := s.Report(7, 1, 50, 100, 0); err != nil {
		t.Fatalf("first valid Report: %v", err)
	}
	if err := s.Report(7, 2, 50, 100, 0); err != nil {
		t.Fatalf("equal now accepted: %v", err)
	}
	if err := s.Report(7, 3, 50, 100, 5); err != nil {
		t.Fatalf("advance clock: %v", err)
	}
	if err := s.Report(7, 4, 50, 100, 4); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock back err=%v, want ErrClockBack", err)
	}
	// 时钟回退被拒后状态不变：seq=4 仍应可接受。
	if err := s.Report(7, 4, 50, 100, 5); err != nil {
		t.Fatalf("rejected op must not consume seq: %v", err)
	}
}

func TestReportStaleSeq(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, 3)
	if err := s.Report(3, 10, 40, 1000, 0); err != nil {
		t.Fatalf("Report: %v", err)
	}
	// 相等也算过期。
	if err := s.Report(3, 10, 40, 1000, 1); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("equal seq err=%v, want ErrStaleReport", err)
	}
	if err := s.Report(3, 9, 40, 1000, 1); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("smaller seq err=%v, want ErrStaleReport", err)
	}
	if err := s.Report(3, 11, 40, 1000, 1); err != nil {
		t.Fatalf("larger seq: %v", err)
	}
	if got := s.Inspect(3).MaxSeq; got != 11 {
		t.Fatalf("maxSeq=%d, want 11", got)
	}
}

func TestReportExpiryAndRevoke(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, 5)
	// p=40, e=1000。
	if err := s.Report(5, 1, 40, 1000, 0); err != nil {
		t.Fatalf("Report: %v", err)
	}
	srv := s.Server(5)
	srv.Settle(999)
	if srv.p != 40 {
		t.Fatalf("t=999 p=%d, want 40", srv.p)
	}
	srv.Settle(1000)
	if srv.p != 0 || srv.d != 0 {
		t.Fatalf("t=1000 expired: p=%d d=%d, want 0/0", srv.p, srv.d)
	}
	// 到期后新通告从 D=0 起算。
	if err := s.Report(5, 2, 30, 500, 1000); err != nil {
		t.Fatalf("Report after expiry: %v", err)
	}
	snap := s.Inspect(5)
	if snap.P != 30 || snap.ExpiresAt != 1500 || snap.D != 0 {
		t.Fatalf("after re-report: %+v", snap)
	}
	// validity=0 撤销：p 置 0、D 清零。
	srv.Settle(1200)
	srv.Arrive(500) // d=30
	if err := s.Report(5, 3, 0, 0, 1200); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	snap = s.Inspect(5)
	if snap.P != 0 || snap.D != 0 {
		t.Fatalf("after revoke: p=%d d=%d, want 0/0", snap.P, snap.D)
	}
}

func TestReportZeroPercentKeepsDeficit(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, 6)
	if err := s.Report(6, 1, 50, 10_000, 0); err != nil {
		t.Fatalf("Report: %v", err)
	}
	srv := s.Server(6)
	srv.Arrive(1000) // d=50
	// 限制仍有效而 p 改为 0：D 保留不清。
	if err := s.Report(6, 2, 0, 10_000, 1); err != nil {
		t.Fatalf("Report p=0: %v", err)
	}
	if snap := s.Inspect(6); snap.P != 0 || snap.D != 50 {
		t.Fatalf("p=0 report must keep D: p=%d d=%d", snap.P, snap.D)
	}
}

func TestDone(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, 9)
	if err := s.Done(9, 0); !errors.Is(err, ErrNoInflight) {
		t.Fatalf("Done with no inflight err=%v, want ErrNoInflight", err)
	}
	if err := s.Done(10, 0); !errors.Is(err, ErrServerNotExist) {
		t.Fatalf("Done unknown server err=%v, want ErrServerNotExist", err)
	}
	srv := s.Server(9)
	srv.Forward()
	srv.Forward()
	if err := s.Done(9, 1); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if got := s.Inspect(9).Inflight; got != 1 {
		t.Fatalf("inflight=%d, want 1", got)
	}
	// 时钟回退优先于无在途。
	if err := s.Done(9, 0); !errors.Is(err, ErrClockBack) {
		t.Fatalf("Done clock back err=%v, want ErrClockBack", err)
	}
	if err := s.Done(9, 1); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if err := s.Done(9, 1); !errors.Is(err, ErrNoInflight) {
		t.Fatalf("Done err=%v, want ErrNoInflight", err)
	}
}

func TestErrorPrecedence(t *testing.T) {
	s := NewStore()
	mustAdd(t, s, 1)
	if err := s.Report(1, 5, 50, 100, 100); err != nil {
		t.Fatalf("Report: %v", err)
	}
	// 参数非法 > 时钟回退：percent 越界且 now 回退。
	if err := s.Report(1, 6, 101, 100, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("param>clock err=%v", err)
	}
	// 时钟回退 > 服务器不存在。
	if err := s.Report(2, 1, 50, 100, 0); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock>notexist err=%v", err)
	}
	// 服务器不存在 > 状态类（通告过期）。
	if err := s.Report(2, 0, 50, 100, 100); !errors.Is(err, ErrServerNotExist) {
		t.Fatalf("notexist>stale err=%v", err)
	}
	// 状态类最后：seq 不更大。
	if err := s.Report(1, 5, 50, 100, 100); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("stale err=%v", err)
	}
}
