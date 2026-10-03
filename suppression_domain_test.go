package ontology

import "testing"

func TestDomainHardWindowStrictlyGreaterThan(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Hard("x@corp.com", 100); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("y@corp.com", 130); err != nil {
		t.Fatal(err)
	}

	dom := manager.domains["corp.com"]
	if dom.until != 0 {
		t.Fatalf("domUntil = %d, want no suppression because 100 is not strictly greater than 130-30", dom.until)
	}
	assertSuppressed(t, manager, "z@corp.com", 130, false, None)
}

func TestDomainSuppressionExemptionAndScanCount(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Hard("x@corp.com", 100); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("other@else.com", 110); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("y@corp.com", 120); err != nil {
		t.Fatal(err)
	}

	dom := manager.domains["corp.com"]
	t.Logf("input=Hard corp addresses at=[100,120] Wd=30 output=domSince=%d domUntil=%d scanned=%d basis=only addresses in corp.com are considered", dom.since, dom.until, manager.lastDomainScanCount())
	if dom.since != 120 || dom.until != 320 {
		t.Fatalf("domain = %+v, want since 120 until 320", dom)
	}
	if got := manager.lastDomainScanCount(); got != len(dom.addresses) || got != 2 {
		t.Fatalf("scan count = %d, want exactly %d corp addresses", got, len(dom.addresses))
	}
	if err := manager.Unsub("z@corp.com", 130); err != nil {
		t.Fatal(err)
	}
	seq, err := manager.RequestConfirm("z@corp.com", 140)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input=RequestConfirm then Confirm seq=%d at=150 output=confirmedAt=150 basis=confirmation at or after domSince exempts address", seq)
	if err := manager.Confirm("z@corp.com", seq, 150); err != nil {
		t.Fatal(err)
	}
	assertSuppressed(t, manager, "other-corp@corp.com", 200, true, Domain)
	assertSuppressed(t, manager, "z@corp.com", 200, false, None)
}

func TestDomainSuppressionExtensionAndRestart(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Hard("x@corp.com", 100); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("y@corp.com", 120); err != nil {
		t.Fatal(err)
	}

	dom := manager.domains["corp.com"]
	if dom.since != 120 || dom.until != 320 {
		t.Fatalf("initial domain = %+v", dom)
	}

	if err := manager.Hard("x@corp.com", 200); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("y@corp.com", 210); err != nil {
		t.Fatal(err)
	}
	t.Logf("input=more hard bounces while active at=[200,210] output=domSince=%d domUntil=%d basis=active suppression extends without restarting", dom.since, dom.until)
	if dom.since != 120 || dom.until != 410 {
		t.Fatalf("extended domain = %+v, want since 120 until 410", dom)
	}

	if err := manager.Hard("x@corp.com", 420); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("y@corp.com", 430); err != nil {
		t.Fatal(err)
	}
	t.Logf("input=more hard bounces after expiry at=[420,430] output=domSince=%d domUntil=%d basis=inactive suppression restarts", dom.since, dom.until)
	if dom.since != 430 || dom.until != 630 {
		t.Fatalf("restarted domain = %+v, want since 430 until 630", dom)
	}
}

func TestConfirmedAtEqualDomainSinceIsExempt(t *testing.T) {
	manager, err := NewManager(2, 10, 100, 50, 4, 30, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("z@corp.com", 100); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("x@corp.com", 100); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("y@corp.com", 110); err != nil {
		t.Fatal(err)
	}

	if err := manager.Hard("w@corp.com", 120); err != nil {
		t.Fatal(err)
	}
	seq, err := manager.RequestConfirm("z@corp.com", 120)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Confirm("z@corp.com", seq, 120); err != nil {
		t.Fatal(err)
	}
	assertSuppressed(t, manager, "z@corp.com", 120, false, None)
}
