package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{A: 30, B: 60, C: 10, D: 15, StepBP: 100, CapBP: 500}
}

func codeOf(err error) ErrCode {
	var ce *CodeError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return -1
}

func mustCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if codeOf(err) != want {
		t.Fatalf("err=%v, want code=%d", err, want)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, c := range []Config{
		{A: -1, B: 60, C: 10, D: 1, StepBP: 100, CapBP: 500},
		{A: 61, B: 60, C: 10, D: 1, StepBP: 100, CapBP: 500},
		{A: 30, B: 60, C: -1, D: 1, StepBP: 100, CapBP: 500},
		{A: 30, B: 60, C: 10, D: -1, StepBP: 100, CapBP: 500},
		{A: 30, B: 60, C: 10, D: 1, StepBP: -1, CapBP: 500},
		{A: 30, B: 60, C: 10, D: 1, StepBP: 100, CapBP: -1},
	} {
		if _, err := NewService(c); err == nil {
			t.Fatalf("expected invalid config: %+v", c)
		}
	}
}

func TestWindowBoundaries(t *testing.T) {
	for _, day := range []int{140, 170} {
		s, _ := NewService(testConfig())
		if err := s.AddLease("L", 0, 200, 10000, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.IssueOffer("L", "O", 10000, 300, day); err != nil {
			t.Fatalf("day %d should be inside window: %v", day, err)
		}
	}
	for _, day := range []int{139, 171} {
		s, _ := NewService(testConfig())
		_ = s.AddLease("L", 0, 200, 10000, 0, 0)
		mustCode(t, s.IssueOffer("L", "O", 10000, 300, day), ErrIllegalState)
	}
}

func TestReplyDeadlineBoundary(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 200, 10000, 0, 0)
	_ = s.IssueOffer("L", "O", 10000, 300, 150)
	if err := s.TenantReply("O", 160, 0, true); err != nil {
		t.Fatalf("deadline day must be accepted: %v", err)
	}

	s2, _ := NewService(testConfig())
	_ = s2.AddLease("L", 0, 200, 10000, 0, 0)
	_ = s2.IssueOffer("L", "O", 10000, 300, 150)
	mustCode(t, s2.TenantReply("O", 161, 0, true), ErrReplyLate)
}

func TestRentCapBoundaries(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 500, 10000, 100, 100)
	mustCode(t, s.IssueOffer("L", "O", 10001, 600, 464), ErrRentAboveCap)
	if err := s.IssueOffer("L", "O", 10000, 600, 464); err != nil {
		t.Fatalf("unchanged rent must be allowed: %v", err)
	}

	s2, _ := NewService(testConfig())
	_ = s2.AddLease("L2", 0, 500, 10000, 100, 100)
	if err := s2.IssueOffer("L2", "O2", 10100, 600, 465); err != nil {
		t.Fatalf("rent exactly at cap must pass: %v", err)
	}

	s3, _ := NewService(testConfig())
	_ = s3.AddLease("L3", 0, 500, 10000, 100, 100)
	mustCode(t, s3.IssueOffer("L3", "O3", 10101, 600, 465), ErrRentAboveCap)

	s4, _ := NewService(testConfig())
	_ = s4.AddLease("L4", 0, 500, 150, 100, 100)
	if err := s4.IssueOffer("L4", "O4", 151, 600, 465); err != nil {
		t.Fatalf("floor allowance 1 must pass: %v", err)
	}

	s5, _ := NewService(testConfig())
	_ = s5.AddLease("L5", 0, 5000, 10000, 100, 100)
	if err := s5.IssueOffer("L5", "O5", 10500, 6000, 4950); err != nil {
		t.Fatalf("capped 5%% must pass: %v", err)
	}

	s6, _ := NewService(testConfig())
	_ = s6.AddLease("L6", 0, 500, 10000, 400, 400)
	if err := s6.IssueOffer("L6", "O6", 1, 600, 464); err != nil {
		t.Fatalf("rent decrease must be unrestricted: %v", err)
	}
}

func TestCounterRentBounds(t *testing.T) {
	big := testConfig()
	big.StepBP, big.CapBP = 100000, 100000
	s, _ := NewService(big)
	_ = s.AddLease("L", 0, 200, 10000, -500, 0)
	_ = s.IssueOffer("L", "O", 11000, 300, 150)
	mustCode(t, s.TenantReply("O", 155, 10000, false), ErrInvalidArgument)
	mustCode(t, s.TenantReply("O", 155, 11000, false), ErrInvalidArgument)
	if err := s.TenantReply("O", 155, 10500, false); err != nil {
		t.Fatalf("strictly between must pass: %v", err)
	}
	mustCode(t, s.TenantReply("O", 156, 10600, false), ErrIllegalState)
	if err := s.LandlordCounterReply("O", 160, true); err != nil {
		t.Fatalf("landlord accept counter at deadline: %v", err)
	}
	v, _ := s.Status("L", 160)
	if v.Rent != 10500 || v.LastAdjust != 200 || v.Start != 200 {
		t.Fatalf("counter renewal wrong: %+v", v)
	}

	s2, _ := NewService(big)
	_ = s2.AddLease("L", 0, 200, 10000, -500, 0)
	_ = s2.IssueOffer("L", "O", 9000, 300, 150)
	mustCode(t, s2.TenantReply("O", 155, 9000, false), ErrInvalidArgument)
	mustCode(t, s2.TenantReply("O", 155, 10000, false), ErrInvalidArgument)
	if err := s2.TenantReply("O", 155, 9500, false); err != nil {
		t.Fatalf("decrease counter between must pass: %v", err)
	}
}

func TestProtectionTriggerAndNot(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 200, 10000, 0, 0)
	mustCode(t, s.IssueOffer("L", "O", 10000, 300, 171), ErrIllegalState)
	v, err := s.Status("L", 200)
	if err != nil {
		t.Fatal(err)
	}
	if v.Phase != PhaseHoldover || v.HoldoverStart != 200 || v.Rent != 10000 {
		t.Fatalf("expected protected holdover: %+v", v)
	}

	s2, _ := NewService(testConfig())
	_ = s2.AddLease("L", 0, 200, 10000, 0, 0)
	_ = s2.IssueOffer("L", "O", 10000, 300, 170)
	v2, err := s2.Status("L", 180)
	if err != nil || v2.Protected || v2.PendingOffer == nil {
		t.Fatalf("offer inside window must prevent protection: %+v err=%v", v2, err)
	}
}

func TestUnchangedRenewalKeepsAccrual(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 500, 10000, 0, 0)
	_ = s.IssueOffer("L", "O", 10000, 900, 465)
	if err := s.TenantReply("O", 470, 0, true); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Status("L", 470)
	if v.LastAdjust != 0 {
		t.Fatalf("unchanged renewal must not reset lastAdjust, got %d", v.LastAdjust)
	}
	if err := s.IssueOffer("L", "O2", 10200, 1200, 850); err != nil {
		t.Fatalf("accrual must carry over: %v", err)
	}

	s2, _ := NewService(testConfig())
	_ = s2.AddLease("L", 0, 500, 10000, 0, 0)
	_ = s2.IssueOffer("L", "O", 10100, 900, 465)
	_ = s2.TenantReply("O", 470, 0, true)
	v2, _ := s2.Status("L", 470)
	if v2.LastAdjust != 500 || v2.Rent != 10100 {
		t.Fatalf("changed renewal must reset lastAdjust: %+v", v2)
	}
}

func TestErrorOrdering(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 200, 10000, 0, 50)
	mustCode(t, s.AddLease("L2", 0, 10, 100, 0, 10), ErrClockRollback)
	mustCode(t, s.AddLease("L2", 0, 10, 0, 0, 60), ErrInvalidArgument)
	mustCode(t, s.IssueOffer("MISSING", "", 1, 1, 10), ErrInvalidArgument)
	mustCode(t, s.IssueOffer("MISSING", "O", 10000, 300, 10), ErrClockRollback)
	mustCode(t, s.IssueOffer("NOPE", "O", 10000, 300, 50), ErrLeaseNotFound)

	_ = s.IssueOffer("L", "O", 10000, 300, 150)
	mustCode(t, s.IssueOffer("L", "O2", 999999, 400, 151), ErrIllegalState)
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 200, 10000, 0, 0)
	_ = s.IssueOffer("L", "O", 10000, 300, 150)

	mustCode(t, s.IssueOffer("L", "O2", 999999, 400, 151), ErrIllegalState)
	mustCode(t, s.WithdrawOffer("O", 140), ErrClockRollback)
	if got := s.LastAcceptedDay(); got != 150 {
		t.Fatalf("clock moved after rejected op: %d", got)
	}

	mustCode(t, s.TenantReply("O", 161, 0, true), ErrReplyLate)
	if err := s.WithdrawOffer("O", 152); err != nil {
		t.Fatalf("rejected late reply must not resolve offer: %v", err)
	}

	after, _ := s.Status("L", 152)
	if after.LastOffer == nil || after.LastOffer.Stage != StageWithdrawn {
		t.Fatalf("offer state unexpected: %+v", after)
	}
}

func TestOverdueOfferBecomesHoldover(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 200, 10000, 0, 0)
	_ = s.IssueOffer("L", "O", 10000, 300, 170)
	v, _ := s.Status("L", 250)
	if v.Phase != PhaseHoldover {
		t.Fatalf("overdue offer must lead to holdover, got %s", v.Phase)
	}
	if err := s.TerminateHoldover("L", 260); err != nil {
		t.Fatal(err)
	}
	v2, _ := s.Status("L", 274)
	if v2.Phase != PhaseHoldover || v2.TerminateAt != 275 {
		t.Fatalf("notice effective day is notice+D: %+v", v2)
	}
	v3, _ := s.Status("L", 275)
	if v3.Phase != PhaseTerminated {
		t.Fatalf("termination must take effect at notice+D, got %s", v3.Phase)
	}
	mustCode(t, s.TerminateHoldover("L", 276), ErrIllegalState)
}

func TestRejectedOfferEndsWithoutHoldover(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 200, 10000, 0, 0)
	_ = s.IssueOffer("L", "O", 10000, 300, 170)
	_ = s.TenantReply("O", 172, 0, false)
	v, _ := s.Status("L", 250)
	if v.Phase != PhaseEnded {
		t.Fatalf("rejected offer should not create holdover, got %s", v.Phase)
	}
}

func TestWithdrawAndReissueInWindow(t *testing.T) {
	s, _ := NewService(testConfig())
	_ = s.AddLease("L", 0, 200, 10000, 0, 0)
	_ = s.IssueOffer("L", "O", 10000, 300, 145)
	if err := s.WithdrawOffer("O", 146); err != nil {
		t.Fatal(err)
	}
	if err := s.IssueOffer("L", "O2", 10000, 310, 147); err != nil {
		t.Fatalf("reissue after withdrawal in window: %v", err)
	}
}

// TestOverdueCostIndependentOfLeaseCount 可验证地证明：
// 判定一份要约逾期所弹出的堆条目数不随租约总数 N 增长（恒为 1）；
// 全量到期时弹出数与“真实到期事件数”同阶（每租约 2 条 + 目标逾期 1 条）。
func TestOverdueCostIndependentOfLeaseCount(t *testing.T) {
	for _, n := range []int{1, 10, 100, 1000} {
		s, _ := NewService(Config{A: 20, B: 60, C: 10, D: 5, StepBP: 100, CapBP: 500})
		for i := 0; i < n; i++ {
			id := "L" + strconv.Itoa(i)
			if err := s.AddLease(id, 0, 500, 10000, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.IssueOffer("L0", "O", 10000, 800, 450); err != nil {
			t.Fatal(err)
		}

		popped := s.sch.peekPop(461)
		if len(popped) != 1 {
			t.Fatalf("n=%d: late judgement popped %d entries, want exactly 1", n, len(popped))
		}
		s.sch.restore(popped)

		all := s.sch.peekPop(500)
		if want := 2*n + 1; len(all) != want {
			t.Fatalf("n=%d: popped %d entries, want %d (two due events per lease + target late)",
				n, len(all), want)
		}
		s.sch.restore(all)
	}
}

func canonicalView(v *LeaseView) string {
	if v == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "start=%d end=%d rent=%d lastAdjust=%d phase=%s protected=%v holdoverStart=%d noticeDay=%d terminateAt=%d",
		v.Start, v.End, v.Rent, v.LastAdjust, v.Phase, v.Protected, v.HoldoverStart, v.NoticeDay, v.TerminateAt)
	if v.PendingOffer != nil {
		fmt.Fprintf(&b, " pending={id=%s rent=%d end=%d issue=%d stage=%s cr=%d cd=%d rd=%d}",
			v.PendingOffer.ID, v.PendingOffer.Rent, v.PendingOffer.NewEnd, v.PendingOffer.IssueDay,
			v.PendingOffer.Stage, v.PendingOffer.CounterRent, v.PendingOffer.CounterDay, v.PendingOffer.ResolveDay)
	}
	if v.LastOffer != nil {
		fmt.Fprintf(&b, " last={id=%s rent=%d end=%d issue=%d stage=%s cr=%d cd=%d rd=%d}",
			v.LastOffer.ID, v.LastOffer.Rent, v.LastOffer.NewEnd, v.LastOffer.IssueDay,
			v.LastOffer.Stage, v.LastOffer.CounterRent, v.LastOffer.CounterDay, v.LastOffer.ResolveDay)
	}
	return b.String()
}

type opKind int

const (
	opAdd opKind = iota
	opIssue
	opWithdraw
	opTenant
	opLandlord
	opTerminate
	opStatus
)

type rec struct {
	kind             opKind
	leaseID, offerID string
	newRent, newEnd  int
	now, counterRent int
	accept           bool
}

func applyOne(s *Service, m *naiveModel, r rec) (sErr, mErr ErrCode, sView, mView string) {
	switch r.kind {
	case opAdd:
		sErr = codeOf(s.AddLease(r.leaseID, r.now, r.now+500, 10000, r.now-900, r.now))
		mErr = codeOf(m.addLease(r.leaseID, r.now, r.now+500, 10000, r.now-900, r.now))
	case opIssue:
		sErr = codeOf(s.IssueOffer(r.leaseID, r.offerID, r.newRent, r.newEnd, r.now))
		mErr = codeOf(m.issueOffer(r.leaseID, r.offerID, r.newRent, r.newEnd, r.now))
	case opWithdraw:
		sErr = codeOf(s.WithdrawOffer(r.offerID, r.now))
		mErr = codeOf(m.withdrawOffer(r.offerID, r.now))
	case opTenant:
		sErr = codeOf(s.TenantReply(r.offerID, r.now, r.counterRent, r.accept))
		mErr = codeOf(m.tenantReply(r.offerID, r.now, r.counterRent, r.accept))
	case opLandlord:
		sErr = codeOf(s.LandlordCounterReply(r.offerID, r.now, r.accept))
		mErr = codeOf(m.landlordCounterReply(r.offerID, r.now, r.accept))
	case opTerminate:
		sErr = codeOf(s.TerminateHoldover(r.leaseID, r.now))
		mErr = codeOf(m.terminateHoldover(r.leaseID, r.now))
	case opStatus:
		var e1, e2 error
		var v1, v2 *LeaseView
		v1, e1 = s.Status(r.leaseID, r.now)
		v2, e2 = m.statusView(r.leaseID, r.now)
		sErr, mErr = codeOf(e1), codeOf(e2)
		sView, mView = canonicalView(v1), canonicalView(v2)
	}
	return
}

var reason = map[ErrCode]string{
	-1:                 "ok",
	ErrInvalidArgument: "参数非法",
	ErrClockRollback:   "时钟回退",
	ErrLeaseNotFound:   "租约/要约不存在",
	ErrIllegalState:    "状态不允许",
	ErrRentAboveCap:    "涨幅超上限",
	ErrReplyLate:       "答复逾期",
}

func opName(k opKind) string {
	names := []string{"AddLease", "IssueOffer", "Withdraw", "TenantReply",
		"LandlordCounter", "TerminateHoldover", "Status"}
	return names[k]
}

// TestDifferentialRandom 用随机操作序列对照堆实现与独立朴素模型，
// 日志逐步打印输入、输出（错误码判定依据）与双方状态视图。
func TestDifferentialRandom(t *testing.T) {
	cfg := Config{A: 20, B: 60, C: 8, D: 12, StepBP: 200, CapBP: 900}
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("random seed=%d", seed)

	for iter := 0; iter < 60; iter++ {
		s, err := NewService(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaiveModel(cfg)

		leaseIDs := []string{"L1", "L2", "L3"}
		offerSeq := 0
		now := 0

		var log strings.Builder
		fmt.Fprintf(&log, "--- iteration %d ---\n", iter)

		for step := 0; step < 300; step++ {
			switch rng.Intn(3) {
			case 1:
				now += rng.Intn(3)
			case 2:
				now += rng.Intn(30)
			}
			r := rec{kind: opKind(rng.Intn(int(opStatus) + 1)), now: now}
			if rng.Intn(20) == 0 {
				r.now = now - 1 - rng.Intn(5)
			}

			r.leaseID = leaseIDs[rng.Intn(len(leaseIDs))]
			if rng.Intn(5) == 0 {
				r.leaseID = "GHOST"
			}
			offerSeq++
			r.offerID = fmt.Sprintf("offer-%d-%d", iter, offerSeq)
			if seq := rng.Intn(offerSeq + 1); seq < offerSeq {
				r.offerID = fmt.Sprintf("offer-%d-%d", iter, seq+1)
			}

			switch r.kind {
			case opIssue:
				modes := []int{10000, 10001, 10200, 10600, 11000, 9999, 9500, 500}
				r.newRent = modes[rng.Intn(len(modes))]
				r.newEnd = r.now + 600 + rng.Intn(400)
			case opTenant:
				r.accept = rng.Intn(2) == 0
				counters := []int{9000, 10000, 10000 + rng.Intn(1500), 9500 + rng.Intn(2000)}
				r.counterRent = counters[rng.Intn(len(counters))]
			case opLandlord:
				r.accept = rng.Intn(2) == 0
			}

			sErr, mErr, sView, mView := applyOne(s, m, r)
			fmt.Fprintf(&log, "step=%03d now=%d op=%s lease=%s offer=%s rent=%d end=%d cr=%d acc=%v -> [%s/%s]",
				step, r.now, opName(r.kind), r.leaseID, r.offerID, r.newRent, r.newEnd,
				r.counterRent, r.accept, reason[sErr], reason[mErr])
			if r.kind == opStatus {
				fmt.Fprintf(&log, "\n  heap : %s\n  naive: %s", sView, mView)
			}
			fmt.Fprintln(&log)

			if sErr != mErr {
				t.Fatalf("iter=%d step=%d error code mismatch: heap=%d naive=%d\n%s",
					iter, step, sErr, mErr, log.String())
			}
			if r.kind == opStatus && sErr == -1 && sView != mView {
				t.Fatalf("iter=%d step=%d view mismatch:\nheap : %s\nnaive: %s\n%s",
					iter, step, sView, mView, log.String())
			}
		}

		for _, id := range leaseIDs {
			for _, day := range []int{now, now + 50, now + 500} {
				v1, e1 := s.Status(id, day)
				v2, e2 := m.statusView(id, day)
				if codeOf(e1) != codeOf(e2) ||
					(e1 == nil && canonicalView(v1) != canonicalView(v2)) {
					t.Fatalf("final view mismatch id=%s day=%d:\nheap :%s\nnaive:%s\n%s",
						id, day, canonicalView(v1), canonicalView(v2), log.String())
				}
			}
		}
		t.Log(log.String())
	}
}

// TestConcurrentSafety 在 -race 下验证线性化：不出现两份未决要约或双接受。
func TestConcurrentSafety(t *testing.T) {
	cfg := Config{A: 10, B: 4000, C: 4000, D: 5, StepBP: 100000, CapBP: 100000}
	s, _ := NewService(cfg)
	if err := s.AddLease("L", 0, 10000, 10000, -1000, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				oid := fmt.Sprintf("o-%d-%d", g, i)
				if err := s.IssueOffer("L", oid, 10000, 20000, 100); err == nil {
					_ = s.TenantReply(oid, 100, 0, true)
				}
			}
		}(g)
	}
	wg.Wait()

	v, _ := s.Status("L", 100)
	if v.PendingOffer != nil && v.LastOffer != nil {
		t.Fatalf("unexpected dual offers: %+v", v)
	}
}
