package contacttracing

import "testing"

// TestMultiCaseStrictestAndLatest 多病例取最严与最晚解除。
func TestMultiCaseStrictestAndLatest(t *testing.T) {
	s := New()
	// 病例 p 在 t=10000 登记：q 密接，最后接触 10000。
	mustOK(t, s.RecordStay("p", "R", 10000, 9880, 10000))
	mustOK(t, s.RecordStay("q", "R", 10000, 9880, 10000))
	_, _ = s.RegisterCase("p", 10000, 10000)
	// 在 15000：密接在期 → 密接隔离中。
	st3, _ := s.PatientStatusAt("q", 15000)
	if st3.Status != StatusCloseQuarantine {
		t.Fatalf("at 15000: %v", st3)
	}
	// 病例 r 在 t=20000 登记：z 密接，q 次密接，最后接触 20000。
	mustOK(t, s.RecordStay("r", "T1", 20000, 19000, 19120))
	mustOK(t, s.RecordStay("z", "T1", 20000, 19000, 19120))
	mustOK(t, s.RecordStay("z", "T2", 20000, 19880, 20000))
	mustOK(t, s.RecordStay("q", "T2", 20000, 19880, 20000))
	_, _ = s.RegisterCase("r", 20000, 19500)
	// 在 21000：密接(至20080)已解除，次密接(至24320)在期 → 次密接观察中。
	st2, _ := s.PatientStatusAt("q", 21000)
	if st2.Status != StatusSecondaryObservation || st2.ReleaseAt != 20000+SecondaryObservationMinutes {
		t.Fatalf("at 21000: %v", st2)
	}
	// 全部过期 → 已解除，解除时刻取最晚 24320。
	st, _ := s.PatientStatusAt("q", 200000)
	if st.Status != StatusReleased || st.ReleaseAt != 20000+SecondaryObservationMinutes {
		t.Fatalf("later now: %v", st)
	}
}

// TestCorrectOnsetMovesContact 改正发病时刻使接触者进出。
func TestCorrectOnsetMovesContact(t *testing.T) {
	s := New()
	now := int64(100000)
	mustOK(t, s.RecordStay("p", "R", now, 1000, 2000))
	mustOK(t, s.RecordStay("q", "R", now, 1000, 2000))
	cid, _ := s.RegisterCase("p", now, 5000) // 传染期 [2120,now)，与住宿无重叠
	entries, _ := s.ListContacts(cid, now)
	if len(entries) != 0 {
		t.Fatalf("expected no contact, got %v", entries)
	}
	mustOK(t, s.CorrectOnset(cid, now, 2000)) // 起点 1712，重叠 [1712,2000)=288 → 密接
	entries, _ = s.ListContacts(cid, now)
	q, ok := findEntry(entries, "q")
	if !ok || q.Kind != KindClose || q.LastContactAt != 2000 {
		t.Fatalf("after correct: %v", entries)
	}
	mustOK(t, s.CorrectOnset(cid, now, 5000))
	entries, _ = s.ListContacts(cid, now)
	if len(entries) != 0 {
		t.Fatalf("expected removed, got %v", entries)
	}
	st, _ := s.PatientStatusAt("q", now)
	if st.Status != StatusUnrelated {
		t.Fatalf("q should be unrelated, got %v", st)
	}
}

// TestBackfillReentersQuarantine 追补使已解除者重新进入隔离。
func TestBackfillReentersQuarantine(t *testing.T) {
	s := New()
	// t=2000 登记：q 密接，最后接触 2000，隔离至 12080。
	mustOK(t, s.RecordStay("p", "R", 2000, 1000, 2000))
	mustOK(t, s.RecordStay("q", "R", 2000, 1000, 2000))
	cid, _ := s.RegisterCase("p", 2000, 2000)
	e, _ := s.ListContacts(cid, 2000)
	if q, _ := findEntry(e, "q"); q.Kind != KindClose {
		t.Fatalf("q not close initially")
	}
	// 20000 时已解除。
	st, _ := s.PatientStatusAt("q", 20000)
	if st.Status != StatusReleased {
		t.Fatalf("expected released, got %v", st)
	}
	// 追补更近的同室住宿（传染期仍开放，未隔离），最后接触推迟到 15000。
	mustOK(t, s.RecordStay("p", "R", 20000, 14000, 15000))
	mustOK(t, s.RecordStay("q", "R", 20000, 14000, 15000))
	e, _ = s.ListContacts(cid, 20000)
	q, _ := findEntry(e, "q")
	if q.LastContactAt != 15000 {
		t.Fatalf("last contact after backfill=%d", q.LastContactAt)
	}
	st, _ = s.PatientStatusAt("q", 20000)
	if st.Status != StatusCloseQuarantine || st.ReleaseAt != 15000+CloseQuarantineMinutes {
		t.Fatalf("expected re-quarantine, got %v", st)
	}
}

// TestRevokeCase 撤销病例后不再产生接触者，再次撤销报状态不符。
func TestRevokeCase(t *testing.T) {
	s := New()
	now := int64(100000)
	mustOK(t, s.RecordStay("p", "R", now, 1000, 2000))
	mustOK(t, s.RecordStay("q", "R", now, 1000, 2000))
	cid, _ := s.RegisterCase("p", now, 2000)
	entries, _ := s.ListContacts(cid, now)
	if len(entries) != 1 {
		t.Fatalf("expected 1 contact, got %v", entries)
	}
	mustOK(t, s.RevokeCase(cid, now))
	entries, _ = s.ListContacts(cid, now)
	if len(entries) != 0 {
		t.Fatalf("after revoke: %v", entries)
	}
	st, _ := s.PatientStatusAt("q", now)
	if st.Status != StatusUnrelated {
		t.Fatalf("q after revoke: %v", st)
	}
	if err := s.RevokeCase(cid, now); errKind(err) != ErrInvalidState {
		t.Fatalf("double revoke err=%v", err)
	}
}
