package contacttracing

import "testing"

// TestThresholdExactly120 与差一分钟。
func TestThresholdExactly120(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     int64
		isClose bool
	}{
		{"exactly_120", 1120, true},
		{"one_minute_short", 1119, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			now := int64(10000)
			mustOK(t, s.RecordStay("p", "R", now, 1000, tc.out))
			mustOK(t, s.RecordStay("q", "R", now, 1000, tc.out))
			cid, err := s.RegisterCase("p", now, 2000)
			mustOK(t, err)
			entries, err := s.ListContacts(cid, now)
			mustOK(t, err)
			e, found := findEntry(entries, "q")
			if found != tc.isClose {
				t.Fatalf("got found=%v want %v (entries=%v)", found, tc.isClose, entries)
			}
			if found && e.Kind != KindClose {
				t.Fatalf("q kind=%v", e.Kind)
			}
			st, _ := s.PatientStatusAt("q", now)
			if tc.isClose && st.Status != StatusCloseQuarantine {
				t.Fatalf("status=%v", st.Status)
			}
			if !tc.isClose && st.Status != StatusUnrelated {
				t.Fatalf("status=%v want unrelated", st.Status)
			}
		})
	}
}

// TestCrossRoomsAccumulate 跨病房分段累计 60+60=120。
func TestCrossRoomsAccumulate(t *testing.T) {
	s := New()
	now := int64(10000)
	mustOK(t, s.RecordStay("p", "A", now, 1000, 1100))
	mustOK(t, s.RecordStay("p", "B", now, 1200, 1260))
	mustOK(t, s.RecordStay("q", "A", now, 1000, 1060))
	mustOK(t, s.RecordStay("q", "B", now, 1200, 1260))
	cid, _ := s.RegisterCase("p", now, 2000)
	entries, err := s.ListContacts(cid, now)
	mustOK(t, err)
	q, ok := findEntry(entries, "q")
	if !ok || q.Kind != KindClose {
		t.Fatalf("q not close: %v", entries)
	}
	if q.LastContactAt != 1260 {
		t.Fatalf("last contact=%d want 1260", q.LastContactAt)
	}
}

// TestInfectiousBoundaries 传染期起点与隔离时刻取等。
func TestInfectiousBoundaries(t *testing.T) {
	s := New()
	now := int64(10000)
	mustOK(t, s.RecordStay("p", "R", now, 0, 5000))
	mustOK(t, s.RecordStay("q", "R", now, 0, 5000))
	cid, _ := s.RegisterCase("p", now, 3000)
	mustOK(t, s.RecordIsolation(cid, now, 1000))
	entries, _ := s.ListContacts(cid, now)
	q, ok := findEntry(entries, "q")
	if !ok || q.Kind != KindClose || q.LastContactAt != 1000 {
		t.Fatalf("q=%v entries=%v", q, entries)
	}
	if err := s.RecordIsolation(cid, now, 1000); errKind(err) != ErrInvalidState {
		t.Fatalf("double isolation err=%v", err)
	}

	// 隔离时刻等于传染期起点，窗口为空，无接触者。
	s2 := New()
	mustOK(t, s2.RecordStay("a", "R", now, 0, 5000))
	mustOK(t, s2.RecordStay("b", "R", now, 0, 5000))
	c2, _ := s2.RegisterCase("a", now, 1000)
	mustOK(t, s2.RecordIsolation(c2, now, 0))
	e2, _ := s2.ListContacts(c2, now)
	if len(e2) != 0 {
		t.Fatalf("empty window gives contacts: %v", e2)
	}
}

// TestReleaseBoundaries 最后接触恰加 7 天/3 天解除。
func TestReleaseBoundaries(t *testing.T) {
	s := New()
	setupNow := int64(2000)
	mustOK(t, s.RecordStay("p", "R", setupNow, 1000, 2000))
	mustOK(t, s.RecordStay("q", "R", setupNow, 1000, 2000))
	cid, _ := s.RegisterCase("p", setupNow, 2000)
	entries, _ := s.ListContacts(cid, setupNow)
	q, _ := findEntry(entries, "q")
	if q.LastContactAt != 2000 {
		t.Fatalf("last=%d", q.LastContactAt)
	}
	boundary := int64(2000 + CloseQuarantineMinutes)
	before, _ := s.PatientStatusAt("q", boundary-1)
	at, _ := s.PatientStatusAt("q", boundary)
	if before.Status != StatusCloseQuarantine || before.ReleaseAt != boundary {
		t.Fatalf("before=%v", before)
	}
	if at.Status != StatusReleased || at.ReleaseAt != boundary {
		t.Fatalf("at=%v", at)
	}

	s2 := New()
	mustOK(t, setupSecondary(s2, 5000))
	secBoundary := int64(5000 + SecondaryObservationMinutes)
	sb, _ := s2.PatientStatusAt("z", secBoundary-1)
	sa, _ := s2.PatientStatusAt("z", secBoundary)
	if sb.Status != StatusSecondaryObservation || sb.ReleaseAt != secBoundary {
		t.Fatalf("sec before=%v", sb)
	}
	if sa.Status != StatusReleased || sa.ReleaseAt != secBoundary {
		t.Fatalf("sec at=%v", sa)
	}
}

// TestExposureEndsAtRegistration 次密接暴露期与确诊登记时刻取等。
func TestExposureEndsAtRegistration(t *testing.T) {
	now := int64(10000)
	s := New()
	mustOK(t, s.RecordStay("p", "R1", now, 1000, 1120))
	mustOK(t, s.RecordStay("q", "R1", now, 1000, 1120))
	mustOK(t, s.RecordStay("q", "R2", now, now-119, now))
	mustOK(t, s.RecordStay("z", "R2", now, now-119, now))
	cid, _ := s.RegisterCase("p", now, 2000)
	entries, _ := s.ListContacts(cid, now)
	if _, ok := findEntry(entries, "z"); ok {
		t.Fatalf("z should not be secondary (119 min): %v", entries)
	}
	s2 := New()
	mustOK(t, s2.RecordStay("p", "R1", now, 1000, 1120))
	mustOK(t, s2.RecordStay("q", "R1", now, 1000, 1120))
	mustOK(t, s2.RecordStay("q", "R2", now, now-120, now))
	mustOK(t, s2.RecordStay("z", "R2", now, now-120, now))
	cid2, _ := s2.RegisterCase("p", now, 2000)
	e2, _ := s2.ListContacts(cid2, now)
	z, ok := findEntry(e2, "z")
	if !ok || z.Kind != KindSecondary || z.LastContactAt != now {
		t.Fatalf("z should be secondary ending exactly at registration: %v", e2)
	}
}
