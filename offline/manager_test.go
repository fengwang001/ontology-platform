package offline_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/errs"
	"ontology/offline"
	"ontology/playback"
)

func newManager(t *testing.T, p offline.Params) *offline.Manager {
	t.Helper()
	m, err := offline.New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func checkErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestInvalidConstruction(t *testing.T) {
	bad := []offline.Params{
		{Dmax: 0, Cool: 100, Lr: 1000, Lp: 50, Omax: 1},
		{Dmax: 101, Cool: 100, Lr: 1000, Lp: 50, Omax: 1},
		{Dmax: 2, Cool: 0, Lr: 1000, Lp: 50, Omax: 1},
		{Dmax: 2, Cool: 100, Lr: 1_000_000_001, Lp: 50, Omax: 1},
		{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 10001},
	}
	for i, p := range bad {
		if _, err := offline.New(p); !errors.Is(err, errs.ErrInvalidParam) {
			t.Fatalf("case %d: want ErrInvalidParam, got %v", i, err)
		}
	}
}

func TestCooldownExactReleaseAndReuse(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.Register(0, "a", "d1"))
	must(t, m.Register(0, "a", "d2"))
	must(t, m.Deregister(10, "a", "d1"))
	checkErr(t, m.Register(20, "a", "d3"), errs.ErrDeviceFull)
	checkErr(t, m.Register(109, "a", "d3"), errs.ErrDeviceFull)
	must(t, m.Register(110, "a", "d3")) // exactly 10+100 releases

	m2 := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m2.AddAccount(0, "a"))
	must(t, m2.Register(0, "a", "d1"))
	must(t, m2.Register(0, "a", "d2"))
	must(t, m2.Deregister(10, "a", "d1"))
	must(t, m2.Register(20, "a", "d1")) // reuse own cooldown slot
	checkErr(t, m2.Register(20, "a", "d3"), errs.ErrDeviceFull)
}

func TestDeregisterDeletesLicensesNoRestore(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 2000))
	must(t, m.Register(0, "a", "d1"))
	must(t, m.Download(0, "a", "d1", "T"))
	must(t, m.Deregister(10, "a", "d1"))
	must(t, m.Register(20, "a", "d1"))
	checkErr(t, m.Play(20, "a", "d1", "T"), errs.ErrNoLicense)
	must(t, m.Deregister(30, "a", "d1"))
	must(t, m.Register(130, "a", "d1")) // after cooldown release
	checkErr(t, m.Play(130, "a", "d1", "T"), errs.ErrNoLicense)
}

func TestPlaybackAcrossRentalAndExactExpiry(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 2000))
	must(t, m.Register(0, "a", "d2"))
	must(t, m.Download(200, "a", "d2", "T"))
	must(t, m.Play(1199, "a", "d2", "T")) // one second before rentalEnd 1200
	st, ok := m.Status("a", "d2", "T", 1199)
	if !ok || st.Status != playback.StatusPlaying || st.Exp != 1249 {
		t.Fatalf("status=%+v ok=%v, want playing exp=1249", st, ok)
	}
	must(t, m.Play(1248, "a", "d2", "T"))
	err := m.Play(1249, "a", "d2", "T") // exactly playback window end
	expErr, isExp := errs.IsExpired(err)
	if !isExp || expErr.Reason != errs.ReasonPlayEnded || expErr.Exp != 1249 {
		t.Fatalf("t=1249 want play-ended exp=1249, got %v", err)
	}

	// never played: expiry exactly at rentalEnd
	m2 := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m2.AddAccount(0, "a"))
	must(t, m2.AddTitle(0, "T", 5000))
	must(t, m2.Register(0, "a", "d"))
	must(t, m2.Download(200, "a", "d", "T"))
	err = m2.Play(1200, "a", "d", "T")
	if expErr, ok := errs.IsExpired(err); !ok || expErr.Reason != errs.ReasonRentalEnded {
		t.Fatalf("unplayed at rentalEnd: want rental-ended, got %v", err)
	}
}

func TestRenewalAndOmaxOnlyCountsValid(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 5000))
	must(t, m.AddTitle(0, "U", 5000))
	must(t, m.Register(0, "a", "d"))
	must(t, m.Download(200, "a", "d", "T"))
	must(t, m.Download(1190, "a", "d", "T")) // unplayed renewal, rentalEnd=2190
	st, _ := m.Status("a", "d", "T", 1190)
	if st.Exp != 2190 {
		t.Fatalf("renewed exp want 2190, got %d", st.Exp)
	}
	checkErr(t, m.Download(1190, "a", "d", "U"), errs.ErrLicenseFull)
	must(t, m.Play(1190, "a", "d", "T"))
	checkErr(t, m.Download(1191, "a", "d", "T"), errs.ErrAlreadyPlayed)
	// expired license frees the slot; expired record is replaced on re-download
	must(t, m.Download(2191, "a", "d", "T"))
}

func TestTitleEndEarlyLateAndNoRevival(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 2000))
	must(t, m.Register(0, "a", "d"))
	must(t, m.Download(200, "a", "d", "T"))
	must(t, m.SetTitleEnd(500, "T", 600)) // early takedown
	err := m.Play(600, "a", "d", "T")
	if expErr, ok := errs.IsExpired(err); !ok || expErr.Reason != errs.ReasonTitleEnded {
		t.Fatalf("early takedown: want title-ended, got %v", err)
	}
	must(t, m.SetTitleEnd(700, "T", 1500)) // delay revives title-only death
	must(t, m.Play(700, "a", "d", "T"))
	st, _ := m.Status("a", "d", "T", 700)
	if st.Exp != 750 {
		t.Fatalf("after revive+play exp want 750, got %d", st.Exp)
	}

	// rental-ended death never revives
	m2 := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m2.AddAccount(0, "a"))
	must(t, m2.AddTitle(0, "T", 5000))
	must(t, m2.Register(0, "a", "d"))
	must(t, m2.Download(0, "a", "d", "T"))
	err = m2.Play(1000, "a", "d", "T")
	if expErr, ok := errs.IsExpired(err); !ok || expErr.Reason != errs.ReasonRentalEnded {
		t.Fatalf("want rental-ended, got %v", err)
	}
	if _, isExp := errs.IsExpired(m2.Play(2000, "a", "d", "T")); !isExp {
		t.Fatalf("rental-ended license must not revive")
	}
}

func TestReasonPrecedence(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 2000))
	must(t, m.Register(0, "a", "d"))
	must(t, m.Download(0, "a", "d", "T"))
	must(t, m.Play(0, "a", "d", "T")) // playEnd=50
	must(t, m.SetTitleEnd(30, "T", 40))
	err := m.Play(60, "a", "d", "T")
	if expErr, ok := errs.IsExpired(err); !ok || expErr.Reason != errs.ReasonTitleEnded {
		t.Fatalf("precedence: want title-ended, got %v", err)
	}
}

func TestDuplicateAdds(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	checkErr(t, m.AddAccount(1, "a"), errs.ErrAccountExists)
	must(t, m.AddTitle(0, "T", 10))
	checkErr(t, m.AddTitle(1, "T", 20), errs.ErrTitleExists)
	checkErr(t, m.SetTitleEnd(5, "T", 5), errs.ErrInvalidParam) // end must be > now
	checkErr(t, m.AddTitle(0, "E", 0), errs.ErrInvalidParam)
}

func TestRejectionOrderingAndNoStateChange(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 1, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(10, "a"))
	must(t, m.AddTitle(10, "T", 5000))
	must(t, m.Register(10, "a", "d"))

	checkErr(t, m.Register(5, "", "x"), errs.ErrInvalidParam)
	checkErr(t, m.Register(5, "a", "x"), errs.ErrClockRewind)
	checkErr(t, m.Register(10, "b", "x"), errs.ErrNoAccount)
	checkErr(t, m.Register(10, "a", "d"), errs.ErrDeviceExists)
	checkErr(t, m.Register(10, "a", "x"), errs.ErrDeviceFull)

	checkErr(t, m.Deregister(5, "a", ""), errs.ErrInvalidParam)
	checkErr(t, m.Deregister(5, "a", "d"), errs.ErrClockRewind)
	checkErr(t, m.Deregister(10, "b", "d"), errs.ErrNoAccount)
	checkErr(t, m.Deregister(10, "a", "nope"), errs.ErrNoDevice)

	checkErr(t, m.Download(5, "a", "d", "T"), errs.ErrClockRewind)
	checkErr(t, m.Download(10, "", "d", "T"), errs.ErrInvalidParam)
	checkErr(t, m.Download(10, "b", "d", "T"), errs.ErrNoAccount)
	checkErr(t, m.Download(10, "a", "d", "Z"), errs.ErrNoTitle)
	checkErr(t, m.Download(10, "a", "ghost", "T"), errs.ErrNoDevice)

	// takedown precedes played-renewal and quota checks
	must(t, m.AddTitle(10, "E", 11))
	checkErr(t, m.Download(11, "a", "d", "E"), errs.ErrTitleEnded)

	checkErr(t, m.Play(5, "a", "d", "T"), errs.ErrClockRewind)
	checkErr(t, m.Play(10, "b", "d", "T"), errs.ErrNoAccount)
	checkErr(t, m.Play(10, "a", "x", "T"), errs.ErrNoDevice)
	checkErr(t, m.Play(10, "a", "d", "Z"), errs.ErrNoTitle)
	checkErr(t, m.Play(10, "a", "d", "T"), errs.ErrNoLicense)

	// a rejected op must not move the clock: now=5 rewind still rejected, then
	// now=10 and later ops behave exactly as before
	checkErr(t, m.Register(10, "a", "d"), errs.ErrDeviceExists)
	checkErr(t, m.Register(9, "a", "d"), errs.ErrClockRewind)
}

func TestSpecRentalExample(t *testing.T) {
	// Lr=1000, Lp=50, titleEnd=2000, Omax=1: renewal then takedown walkthrough
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 2000))
	must(t, m.Register(0, "a", "d2"))
	must(t, m.Download(200, "a", "d2", "T"))
	must(t, m.Download(1190, "a", "d2", "T")) // rentalEnd=2190, exp=min(2190,2000)=2000
	st, _ := m.Status("a", "d2", "T", 1190)
	if st.Exp != 2000 {
		t.Fatalf("exp want 2000, got %d", st.Exp)
	}
	err := m.Play(2000, "a", "d2", "T")
	if expErr, ok := errs.IsExpired(err); !ok || expErr.Reason != errs.ReasonTitleEnded {
		t.Fatalf("want title-ended at 2000, got %v", err)
	}
	must(t, m.SetTitleEnd(1500, "T", 2100))
	must(t, m.Play(2000, "a", "d2", "T")) // extended title: playable at 2000
}

func TestFirstPlayImmutable(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 2, Cool: 100, Lr: 1000, Lp: 50, Omax: 1})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 5000))
	must(t, m.Register(0, "a", "d"))
	must(t, m.Download(0, "a", "d", "T"))
	must(t, m.Play(100, "a", "d", "T"))
	must(t, m.Play(110, "a", "d", "T"))
	st, _ := m.Status("a", "d", "T", 110)
	if st.Exp != 150 { // firstPlay 100 + 50, unchanged by later play
		t.Fatalf("firstPlay must be immutable, exp=%d", st.Exp)
	}
}

func TestPlayTouchedIsOne(t *testing.T) {
	m := newManager(t, offline.Params{Dmax: 10, Cool: 100, Lr: 1000, Lp: 50, Omax: 10})
	must(t, m.AddAccount(0, "a"))
	must(t, m.AddTitle(0, "T", 5000))
	must(t, m.Register(0, "a", "d"))
	must(t, m.Download(0, "a", "d", "T"))
	must(t, m.Play(10, "a", "d", "T"))
	if got := m.LicenseTouched(); got != 1 {
		t.Fatalf("Play touched=%d, want 1", got)
	}
	must(t, m.Play(20, "a", "d", "T"))
	if got := m.LicenseTouched(); got != 1 {
		t.Fatalf("repeat Play touched=%d, want 1", got)
	}
}

func TestDeviceTouchedBound(t *testing.T) {
	// 3 devices deregistered at staggered times leave 3 unreleased slots; the
	// rejected registration touches at most #unreleased + 1.
	m := newManager(t, offline.Params{Dmax: 4, Cool: 1000, Lr: 1000, Lp: 50, Omax: 4})
	must(t, m.AddAccount(0, "a"))
	for _, d := range []string{"d1", "d2", "d3", "d4"} {
		must(t, m.Register(0, "a", d))
	}
	must(t, m.Deregister(10, "a", "d1"))
	must(t, m.Deregister(20, "a", "d2"))
	must(t, m.Deregister(30, "a", "d3"))
	// now 40: 3 unreleased cooldown slots, d4 still registered => full
	checkErr(t, m.Register(40, "a", "new"), errs.ErrDeviceFull)
	if got := m.DeviceTouched(); got > 3+1 {
		t.Fatalf("device touched=%d, want <= unreleased(3)+1", got)
	}
	// reusing own slot stops at that record (first here): touched==1
	must(t, m.Register(40, "a", "d1"))
	if got := m.DeviceTouched(); got != 1 {
		t.Fatalf("own-slot reuse touched=%d, want 1", got)
	}
}

func TestTouchedIsolation(t *testing.T) {
	for _, other := range []int{100, 10000} {
		m := newManager(t, offline.Params{Dmax: 100, Cool: 100, Lr: 1000, Lp: 1000, Omax: 10000})
		must(t, m.AddAccount(0, "a"))
		must(t, m.AddAccount(0, "b"))
		for i := 0; i < 100; i++ {
			tn := fmtTitle(i)
			must(t, m.AddTitle(0, tn, 1_000_000))
		}
		// account b holds `other` licenses across many devices
		devsNeeded := (other + 99) / 100
		for i := 0; i < devsNeeded; i++ {
			must(t, m.Register(0, "b", fmt.Sprintf("bd%d", i)))
		}
		for i := 0; i < other; i++ {
			must(t, m.Download(0, "b", fmt.Sprintf("bd%d", i/100), fmtTitle(i%100)))
		}
		must(t, m.Register(0, "a", "ad"))
		must(t, m.Download(0, "a", "ad", fmtTitle(0)))
		got := m.LicenseTouched()
		// account a has exactly 1 license record; touched <= records+1 == 2
		if got > 2 {
			t.Fatalf("other=%d touched=%d, want <= 2", other, got)
		}
	}
}

func fmtTitle(i int) string { return fmt.Sprintf("T%04d", i) }
