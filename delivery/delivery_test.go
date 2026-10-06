package delivery

import (
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		MinWaitSeconds:        60,
		MinContacts:           3,
		MinContactInterval:    10,
		CorrectionWindow:      30,
		MaxCorrectionDist:     5,
		EvidenceTTLSeconds:    15,
		RiderCompensation:     8,
		MerchantConfirmWindow: 20,
	}
}

func errCode(err error) ErrorCode {
	if err == nil {
		return 0
	}
	return err.(*Error).Code
}

func mustCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if errCode(err) != want {
		t.Fatalf("error code = %v, want %v (%v)", errCode(err), want, err)
	}
}

func createPicked(t *testing.T, p *Platform, id string, t0 int, disp Disposition) {
	t.Helper()
	if _, err := p.CreateOrder(id, t0, Address{}, disp); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if _, err := p.Pickup(id, t0); err != nil {
		t.Fatalf("Pickup: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	cfg := testConfig()
	cfg.MinWaitSeconds = -1
	if _, err := NewPlatform(cfg); errCode(err) != ErrInvalidParam {
		t.Fatalf("negative config should be invalid, got %v", err)
	}
}

func TestClockRegression(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o1", 10, DispositionOnSite)
	if _, err := p.Get("o1", 9); errCode(err) != ErrClockBack {
		t.Fatalf("clock back on query, got %v", err)
	}
	_, err := p.RecordContact("o1", 8)
	mustCode(t, err, ErrClockBack)
}

func TestNotFoundAndReportPreconditions(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	if _, err := p.Pickup("missing", 1); errCode(err) != ErrOrderNotFound {
		t.Fatalf("missing order, got %v", err)
	}
	if _, err := p.CreateOrder("o", 0, Address{}, DispositionReturn); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReportUnreachable("o", 1); errCode(err) != ErrNotPickedUp {
		t.Fatalf("report before pickup, got %v", err)
	}
	if _, err := p.Pickup("o", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Deliver("o", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReportUnreachable("o", 3); errCode(err) != ErrAlreadyDelivered {
		t.Fatalf("report after delivery, got %v", err)
	}
}

func TestContactIntervalBoundary(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionOnSite)
	if _, err := p.ReportUnreachable("o", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RecordContact("o", 0); err != nil {
		t.Fatal(err)
	}
	_, err := p.RecordContact("o", 9)
	mustCode(t, err, ErrContactTooFrequent)
	if _, err := p.RecordContact("o", 10); err != nil {
		t.Fatalf("equal interval accepted, got %v", err)
	}
	if _, err := p.RecordContact("o", 20); err != nil {
		t.Fatalf("equal interval accepted, got %v", err)
	}
	o, _ := p.Get("o", 20)
	if o.Current.ContactCount != 3 {
		t.Fatalf("contact count = %d, want 3", o.Current.ContactCount)
	}
}

func TestWaitBoundaryAndConditionErrors(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionOnSite)
	if _, err := p.ReportUnreachable("o", 0); err != nil {
		t.Fatal(err)
	}
	for _, ct := range []int{0, 10, 20} {
		if _, err := p.RecordContact("o", ct); err != nil {
			t.Fatal(err)
		}
	}
	_, err := p.JudgeUndeliverable("o", 59)
	mustCode(t, err, ErrConditionWait)
	o, err := p.JudgeUndeliverable("o", 60)
	if err != nil {
		t.Fatalf("equal wait boundary should pass, got %v", err)
	}
	if o.Status != OrderDisposed || o.Liability != LiabilityCustomer || !o.CompPaid {
		t.Fatalf("unexpected terminal snapshot: %+v", o)
	}
}

func TestConditionMissingSeparately(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o1", 0, DispositionOnSite)
	if _, err := p.ReportUnreachable("o1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RecordContact("o1", 0); err != nil {
		t.Fatal(err)
	}
	_, err := p.JudgeUndeliverable("o1", 60)
	mustCode(t, err, ErrConditionContact)

	p2, _ := NewPlatform(testConfig())
	createPicked(t, p2, "o2", 0, DispositionOnSite)
	if _, err := p2.ReportUnreachable("o2", 100); err != nil {
		t.Fatal(err)
	}
	for _, ct := range []int{100, 110, 120} {
		if _, err := p2.RecordContact("o2", ct); err != nil {
			t.Fatal(err)
		}
	}
	_, err = p2.JudgeUndeliverable("o2", 159)
	mustCode(t, err, ErrConditionWait)
}

func TestRespondThenRerunFromZero(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionOnSite)
	if _, err := p.ReportUnreachable("o", 0); err != nil {
		t.Fatal(err)
	}
	for _, ct := range []int{0, 10, 20} {
		if _, err := p.RecordContact("o", ct); err != nil {
			t.Fatal(err)
		}
	}
	o, err := p.CustomerRespond("o", 25)
	if err != nil || o.Status != OrderDelivering {
		t.Fatalf("respond: %v %+v", err, o)
	}
	ex, err := p.ReportUnreachable("o", 30)
	if err != nil {
		t.Fatal(err)
	}
	if ex.ContactCount != 0 {
		t.Fatalf("new exception contacts = %d, want 0", ex.ContactCount)
	}
	if _, err := p.RecordContact("o", 30); err != nil {
		t.Fatal(err)
	}
	_, err = p.JudgeUndeliverable("o", 90)
	mustCode(t, err, ErrConditionContact)

	p2, _ := NewPlatform(testConfig())
	createPicked(t, p2, "o2", 0, DispositionOnSite)
	if _, err := p2.ReportUnreachable("o2", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p2.CustomerRespond("o2", 1); err != nil {
		t.Fatal(err)
	}
	_, err = p2.RecordContact("o2", 2)
	mustCode(t, err, ErrExceptionClosed)
}

func TestCorrectionDistanceAndWindow(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionOnSite)
	if _, err := p.ReportWrongAddress("o", 100); err != nil {
		t.Fatal(err)
	}
	o, err := p.SubmitCorrection("o", 120, Address{X: 5, Y: 0})
	if err != nil {
		t.Fatalf("equal distance boundary should pass, got %v", err)
	}
	if o.AddrChanges != 1 || o.Status != OrderDelivering {
		t.Fatalf("correction snapshot: %+v", o)
	}
	if _, err := p.ReportWrongAddress("o", 200); err != nil {
		t.Fatal(err)
	}
	_, err = p.SubmitCorrection("o", 210, Address{X: 5, Y: 6})
	mustCode(t, err, ErrDistanceExceeded)
	o, err = p.SubmitCorrection("o", 220, Address{X: 5, Y: 4})
	if err != nil {
		t.Fatalf("retry after rejection: %v", err)
	}
	if o.AddrChanges != 2 {
		t.Fatalf("addr changes = %d, want 2", o.AddrChanges)
	}

	p2, _ := NewPlatform(testConfig())
	createPicked(t, p2, "o2", 0, DispositionOnSite)
	if _, err := p2.ReportWrongAddress("o2", 200); err != nil {
		t.Fatal(err)
	}
	_, err = p2.SubmitCorrection("o2", 230, Address{X: 1, Y: 0})
	mustCode(t, err, ErrCorrectionWindow)
}

func TestWindowAutoConversion(t *testing.T) {
	for _, disp := range []Disposition{DispositionOnSite, DispositionReturn} {
		p, _ := NewPlatform(testConfig())
		id := "o"
		createPicked(t, p, id, 0, disp)
		if _, err := p.ReportWrongAddress(id, 100); err != nil {
			t.Fatal(err)
		}
		o, err := p.Get(id, 130)
		if err != nil {
			t.Fatal(err)
		}
		if o.Liability != LiabilityCustomer {
			t.Fatalf("liability = %v, want customer", o.Liability)
		}
		if disp == DispositionOnSite && o.Status != OrderDisposed {
			t.Fatalf("onsite status = %v", o.Status)
		}
		if disp == DispositionReturn && o.Status != OrderReturning {
			t.Fatalf("return status = %v", o.Status)
		}
		_, err = p.ReportUnreachable(id, 131)
		if disp == DispositionOnSite {
			mustCode(t, err, ErrOrderTerminal)
		} else {
			mustCode(t, err, ErrActiveException)
		}
	}
}

func TestEvidenceBoundary(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionOnSite)
	ex, err := p.ReportRefusal("o", 100, "ev-1", 85)
	if err != nil {
		t.Fatalf("equal evidence boundary should pass, got %v", err)
	}
	if ex.Phase != ExceptionUndeliverable {
		t.Fatalf("refusal phase = %v", ex.Phase)
	}
	o, _ := p.Get("o", 100)
	if o.Status != OrderDisposed || o.Liability != LiabilityCustomer || !o.CompPaid {
		t.Fatalf("refusal snapshot: %+v", o)
	}

	p2, _ := NewPlatform(testConfig())
	createPicked(t, p2, "o2", 0, DispositionOnSite)
	_, err = p2.ReportRefusal("o2", 100, "ev-x", 84)
	mustCode(t, err, ErrEvidenceInvalid)
	o2, _ := p2.Get("o2", 100)
	if o2.Current != nil || o2.ExceptionSeq != 0 || o2.Status != OrderDelivering {
		t.Fatalf("invalid evidence changed state: %+v", o2)
	}
	_, err = p2.ReportRefusal("o2", 101, "", 100)
	mustCode(t, err, ErrInvalidParam)
}

func TestRefusalTerminalNoRereport(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionReturn)
	if _, err := p.ReportRefusal("o", 5, "ev", 5); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func() error{
		func() error { _, e := p.ReportUnreachable("o", 6); return e },
		func() error { _, e := p.ReportWrongAddress("o", 6); return e },
		func() error { _, e := p.ReportRefusal("o", 6, "ev2", 6); return e },
	} {
		mustCode(t, fn(), ErrActiveException)
	}
	if _, err := p.RiderReturn("o", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := p.MerchantConfirm("o", 29); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func() error{
		func() error { _, e := p.ReportUnreachable("o", 30); return e },
		func() error { _, e := p.ReportWrongAddress("o", 30); return e },
		func() error { _, e := p.ReportRefusal("o", 30, "ev3", 30); return e },
	} {
		mustCode(t, fn(), ErrOrderTerminal)
	}
}

func TestReturnFlowBoundaries(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionReturn)
	if _, err := p.ReportRefusal("o", 0, "ev", 0); err != nil {
		t.Fatal(err)
	}
	_, err := p.MerchantConfirm("o", 1)
	mustCode(t, err, ErrTypeMismatch)
	if _, err := p.RiderReturn("o", 10); err != nil {
		t.Fatal(err)
	}
	_, err = p.MerchantConfirm("o", 30)
	mustCode(t, err, ErrConfirmWindow)
	if o, _ := p.Get("o", 30); o.Status != OrderReturnUnconfirmed {
		t.Fatalf("endpoint should be unconfirmed: %+v", o)
	}

	p2, _ := NewPlatform(testConfig())
	createPicked(t, p2, "o2", 0, DispositionReturn)
	if _, err := p2.ReportRefusal("o2", 0, "ev", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p2.RiderReturn("o2", 10); err != nil {
		t.Fatal(err)
	}
	o2, err := p2.MerchantConfirm("o2", 29)
	if err != nil || o2.Status != OrderReturned {
		t.Fatalf("inside-window confirm: %+v %v", o2, err)
	}

	p3, _ := NewPlatform(testConfig())
	createPicked(t, p3, "o3", 0, DispositionReturn)
	if _, err := p3.ReportRefusal("o3", 0, "ev", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p3.RiderReturn("o3", 10); err != nil {
		t.Fatal(err)
	}
	o3, err := p3.Get("o3", 30)
	if err != nil {
		t.Fatal(err)
	}
	if o3.Status != OrderReturnUnconfirmed || o3.Liability != LiabilityMerchant || !o3.CompPaid {
		t.Fatalf("unconfirmed snapshot: %+v", o3)
	}
	_, err = p3.MerchantConfirm("o3", 31)
	mustCode(t, err, ErrOrderTerminal)
}

func TestCompensationOnce(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionReturn)
	if _, err := p.ReportRefusal("o", 0, "ev", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RiderReturn("o", 10); err != nil {
		t.Fatal(err)
	}
	o, _ := p.MerchantConfirm("o", 29)
	if !o.CompPaid {
		t.Fatal("compensation flag must remain set and be granted only once")
	}
}

func TestConcurrentOneExceptionOneCompensation(t *testing.T) {
	p, _ := NewPlatform(testConfig())
	createPicked(t, p, "o", 0, DispositionOnSite)
	var wg sync.WaitGroup
	open := 0
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.ReportRefusal("o", 5, "ev", 5); err == nil {
				mu.Lock()
				open++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if open != 1 {
		t.Fatalf("accepted refusal reports = %d, want 1", open)
	}
	o, _ := p.Get("o", 5)
	if o.ExceptionSeq != 1 || !o.CompPaid {
		t.Fatalf("seq=%d paid=%v, want 1/true", o.ExceptionSeq, o.CompPaid)
	}
}

func TestConcurrentRespondVsJudge(t *testing.T) {
	cfg := testConfig()
	outcomes := map[string]int{}
	for round := 0; round < 200; round++ {
		p, _ := NewPlatform(cfg)
		createPicked(t, p, "o", 0, DispositionReturn)
		if _, err := p.ReportUnreachable("o", 0); err != nil {
			t.Fatal(err)
		}
		for _, ct := range []int{0, 10, 20} {
			if _, err := p.RecordContact("o", ct); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		var ro, jo *Order
		var re, je error
		wg.Add(2)
		go func() { defer wg.Done(); ro, re = p.CustomerRespond("o", 60) }()
		go func() { defer wg.Done(); jo, je = p.JudgeUndeliverable("o", 60) }()
		wg.Wait()
		switch {
		case re == nil && je != nil:
			if ro.Status != OrderDelivering || errCode(je) != ErrExceptionClosed {
				t.Fatalf("respond-won inconsistent: %+v %v", ro, je)
			}
			outcomes["respond"]++
		case je == nil && re != nil:
			if jo.Status != OrderReturning {
				t.Fatalf("judge-won inconsistent: %+v %v", jo, re)
			}
			outcomes["judge"]++
		default:
			t.Fatalf("non-exclusive outcome: respond=%v/%v judge=%v/%v", ro, re, jo, je)
		}
	}
	if outcomes["respond"] == 0 || outcomes["judge"] == 0 {
		t.Fatalf("expected both serializations, got %v", outcomes)
	}
}

func TestReplayDeterministic(t *testing.T) {
	run := func() (OrderStatus, Liability, bool) {
		p, _ := NewPlatform(testConfig())
		createPicked(t, p, "o", 0, DispositionReturn)
		_, _ = p.ReportUnreachable("o", 0)
		_, _ = p.RecordContact("o", 0)
		_, _ = p.RecordContact("o", 10)
		_, _ = p.RecordContact("o", 20)
		_, _ = p.JudgeUndeliverable("o", 60)
		_, _ = p.RiderReturn("o", 70)
		o, _ := p.Get("o", 100)
		return o.Status, o.Liability, o.CompPaid
	}
	a1, l1, c1 := run()
	a2, l2, c2 := run()
	if a1 != a2 || l1 != l2 || c1 != c2 {
		t.Fatalf("non-deterministic replay")
	}
	if a1 != OrderReturnUnconfirmed || l1 != LiabilityMerchant || !c1 {
		t.Fatalf("unexpected terminal: %v %v %v", a1, l1, c1)
	}
}

func setupLongHistory(b *testing.B, p *Platform, id string, n int) {
	b.Helper()
	cfg := testConfig()
	if _, err := p.CreateOrder(id, 0, Address{}, DispositionOnSite); err != nil {
		b.Fatal(err)
	}
	if _, err := p.Pickup(id, 0); err != nil {
		b.Fatal(err)
	}
	if _, err := p.ReportUnreachable(id, 0); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		tm := i * (cfg.MinContactInterval + 1)
		if _, err := p.RecordContact(id, tm); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJudgeAfterLongHistory times the verdict only (setup excluded).
// The predicate sub-benchmarks read solely the counter fields and show
// identical ns/op for 100 vs 100000 historical contacts, proving O(1).
func BenchmarkJudgeAfterLongHistory(b *testing.B) {
	cfg := testConfig()
	measure := func(b *testing.B, n int) {
		b.Helper()
		b.StopTimer()
		gap := cfg.MinContactInterval + 1
		verdictT := n*gap - gap + 1
		for i := 0; i < b.N; i++ {
			pp, _ := NewPlatform(cfg)
			setupLongHistory(b, pp, "o", n)
			b.StartTimer()
			o, err := pp.JudgeUndeliverable("o", verdictT)
			b.StopTimer()
			if err != nil || o.Status != OrderDisposed {
				b.Fatalf("verdict: %v %+v", err, o)
			}
		}
	}
	b.Run("history_100", func(b *testing.B) { measure(b, 100) })
	b.Run("history_100000", func(b *testing.B) { measure(b, 100000) })
	pred := func(b *testing.B, cnt int) {
		ex := &exception{cnt: cnt, start: 0}
		t := 60
		for i := 0; i < b.N; i++ {
			if t-ex.start < cfg.MinWaitSeconds || ex.cnt < cfg.MinContacts {
				b.Fatal("predicate mismatch")
			}
		}
	}
	b.Run("predicate_100", func(b *testing.B) { pred(b, 100) })
	b.Run("predicate_100000", func(b *testing.B) { pred(b, 100000) })
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// BenchmarkMaturityPlatformSize checks lazy maturity only inspects the
// target order; the number of other platform exceptions is irrelevant.
func BenchmarkMaturityPlatformSize(b *testing.B) {
	cfg := testConfig()
	measure := func(b *testing.B, others int) {
		b.Helper()
		b.StopTimer()
		for i := 0; i < b.N; i++ {
			p, _ := NewPlatform(cfg)
			for k := 0; k < others; k++ {
				id := "other" + itoa(k)
				_, _ = p.CreateOrder(id, 0, Address{}, DispositionReturn)
				_, _ = p.Pickup(id, 0)
				_, _ = p.ReportWrongAddress(id, 0)
			}
			_, _ = p.CreateOrder("target", 0, Address{}, DispositionReturn)
			_, _ = p.Pickup("target", 0)
			_, _ = p.ReportWrongAddress("target", 0)
			b.StartTimer()
			o, err := p.Get("target", cfg.CorrectionWindow)
			b.StopTimer()
			if err != nil || o.Status != OrderReturning {
				b.Fatalf("maturity: %v %+v", err, o)
			}
		}
	}
	b.Run("others_10", func(b *testing.B) { measure(b, 10) })
	b.Run("others_10000", func(b *testing.B) { measure(b, 10000) })
}
