package campaign

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func mustNew(t *testing.T, T int, M []int, C, R int, B, D int64, F int) *Campaign {
	t.Helper()
	c, err := New(T, M, C, R, B, D, F)
	if err != nil {
		t.Fatalf("New(%d,%v,%d,%d,%d,%d,%d): %v", T, M, C, R, B, D, F, err)
	}
	return c
}

func mustAdd(t *testing.T, c *Campaign, now int64, id string, v int) {
	t.Helper()
	if err := c.AddDevice(now, []byte(id), v); err != nil {
		t.Fatalf("AddDevice(%d,%q,%d): %v", now, id, v, err)
	}
}

func dev(t *testing.T, c *Campaign, id string) *device {
	t.Helper()
	d, ok := c.devices[id]
	if !ok {
		t.Fatalf("device %q missing", id)
	}
	return d
}

func wantItems(t *testing.T, got []Item, err error, want []Item) {
	t.Helper()
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Dispatch items = %v, want %v", got, want)
	}
}

// TestSpecExample walks the worked example from the specification:
// T=6, M={3,5}, C=2, R=2, B=10, D=100, F=2.
func TestSpecExample(t *testing.T) {
	c := mustNew(t, 6, []int{3, 5}, 2, 2, 10, 100, 2)
	mustAdd(t, c, 0, "a", 1)
	mustAdd(t, c, 0, "b", 3)
	mustAdd(t, c, 0, "c", 5)
	mustAdd(t, c, 0, "d", 6)
	if got := dev(t, c, "d").state; got != Skipped {
		t.Fatalf("d state = %v, want Skipped", got)
	}

	items, err := c.Dispatch(0, 5)
	wantItems(t, items, err, []Item{{[]byte("a"), 3, 1}, {[]byte("b"), 5, 2}})
	if got := dev(t, c, "c").state; got != Pending {
		t.Fatalf("c state = %v, want Pending (slots exhausted)", got)
	}

	if err := c.Report(10, []byte("a"), 1, 3, true); err != nil {
		t.Fatalf("Report a: %v", err)
	}
	if d := dev(t, c, "a"); d.ver != 3 || d.state != Pending || d.readyAt != 10 || d.attempts != 0 {
		t.Fatalf("a = %+v, want ver=3 Pending readyAt=10 attempts=0", d)
	}

	items, err = c.Dispatch(10, 5)
	wantItems(t, items, err, []Item{{[]byte("c"), 6, 3}})

	// Entry settlement: b times out at dl=100 (equal counts), failure
	// time is dl, so readyAt = 100 + 10*1 = 110.
	items, err = c.Dispatch(100, 1)
	wantItems(t, items, err, []Item{{[]byte("a"), 5, 4}})
	if d := dev(t, c, "b"); d.state != Pending || d.attempts != 1 || d.readyAt != 110 {
		t.Fatalf("b = %+v, want Pending attempts=1 readyAt=110", d)
	}

	// c's dl=110 is reached: the report is rejected with ErrNotInFlight
	// and the settlement is rolled back, so c stays InFlight.
	if err := c.Report(110, []byte("c"), 3, 6, true); !errors.Is(err, ErrNotInFlight) {
		t.Fatalf("Report c = %v, want ErrNotInFlight", err)
	}
	if d := dev(t, c, "c"); d.state != InFlight || d.attempts != 0 || d.tok != 3 {
		t.Fatalf("c = %+v, want InFlight attempts=0 tok=3 (settlement rolled back)", d)
	}
	if got := c.slots.Used(); got != 2 {
		t.Fatalf("slots used = %d, want 2 (rollback released nothing)", got)
	}

	// The next accepted operation lands the timeout: c fails at dl=110,
	// readyAt = 110 + 10*1 = 120; b (readyAt=110) is dispatched next.
	items, err = c.Dispatch(110, 1)
	wantItems(t, items, err, []Item{{[]byte("b"), 5, 5}})
	if d := dev(t, c, "c"); d.state != Pending || d.attempts != 1 || d.readyAt != 120 {
		t.Fatalf("c = %+v, want Pending attempts=1 readyAt=120", d)
	}
}

// TestAbortInflightOutcomes covers the second specification example:
// with R=1, F=1 the first failure aborts the campaign; InFlight devices
// keep running and end Done / Cancelled / Failed, never Pending again.
func TestAbortInflightOutcomes(t *testing.T) {
	c := mustNew(t, 6, []int{3}, 4, 1, 10, 1000, 1)
	mustAdd(t, c, 0, "a", 1)
	mustAdd(t, c, 0, "b", 1)
	mustAdd(t, c, 0, "c", 1)
	mustAdd(t, c, 0, "e", 1)
	mustAdd(t, c, 0, "d", 1)
	items, err := c.Dispatch(0, 4)
	wantItems(t, items, err, []Item{
		{[]byte("a"), 3, 1},
		{[]byte("b"), 3, 2},
		{[]byte("c"), 3, 3},
		{[]byte("d"), 3, 4},
	})

	// a fails with R=1: Failed, and failed count reaches F=1 -> abort.
	if err := c.Report(10, []byte("a"), 1, 0, false); err != nil {
		t.Fatalf("Report a: %v", err)
	}
	if !c.aborted {
		t.Fatal("campaign not aborted after first Failed")
	}
	if got := dev(t, c, "a").state; got != Failed {
		t.Fatalf("a state = %v, want Failed", got)
	}
	if got := dev(t, c, "e").state; got != Cancelled {
		t.Fatalf("e state = %v, want Cancelled (Pending at abort)", got)
	}

	// Dispatch after abort is rejected; AddDevice still works and the
	// new device is Cancelled directly.
	if _, err := c.Dispatch(10, 1); !errors.Is(err, ErrAborted) {
		t.Fatalf("Dispatch = %v, want ErrAborted", err)
	}
	mustAdd(t, c, 20, "z", 1)
	if got := dev(t, c, "z").state; got != Cancelled {
		t.Fatalf("z state = %v, want Cancelled (added after abort)", got)
	}
	mustAdd(t, c, 20, "w", 6)
	if got := dev(t, c, "w").state; got != Skipped {
		t.Fatalf("w state = %v, want Skipped (v >= T even after abort)", got)
	}

	// InFlight b reaches mandatory 3 (not T): Cancelled, version kept.
	if err := c.Report(30, []byte("b"), 2, 3, true); err != nil {
		t.Fatalf("Report b: %v", err)
	}
	if d := dev(t, c, "b"); d.state != Cancelled || d.ver != 3 {
		t.Fatalf("b = %+v, want Cancelled ver=3", d)
	}

	// InFlight c reaches T: Done.
	if err := c.Report(30, []byte("c"), 3, 6, true); !errors.Is(err, ErrVersion) {
		t.Fatalf("Report c wrong ver = %v, want ErrVersion", err)
	}
	// c's hop is 3, not 6; walk it there via a fresh hop-less path:
	// after abort there is no re-dispatch, so c can only reach its hop.
	if err := c.Report(30, []byte("c"), 3, 3, true); err != nil {
		t.Fatalf("Report c: %v", err)
	}
	if d := dev(t, c, "c"); d.state != Cancelled || d.ver != 3 {
		t.Fatalf("c = %+v, want Cancelled ver=3", d)
	}

	// InFlight d fails: Failed, still counted after abort.
	if err := c.Report(40, []byte("d"), 4, 0, false); err != nil {
		t.Fatalf("Report d: %v", err)
	}
	if d := dev(t, c, "d"); d.state != Failed {
		t.Fatalf("d = %+v, want Failed", d)
	}
	if c.failed != 2 {
		t.Fatalf("failed = %d, want 2", c.failed)
	}
}

// TestAbortInflightReachesDone checks an InFlight device whose hop is T
// ends Done after abort.
func TestAbortInflightReachesDone(t *testing.T) {
	c := mustNew(t, 6, []int{3}, 2, 1, 10, 1000, 1)
	mustAdd(t, c, 0, "a", 1)
	mustAdd(t, c, 0, "b", 5)
	items, err := c.Dispatch(0, 2)
	wantItems(t, items, err, []Item{{[]byte("a"), 3, 1}, {[]byte("b"), 6, 2}})
	if err := c.Report(10, []byte("a"), 1, 0, false); err != nil {
		t.Fatalf("Report a: %v", err)
	}
	if !c.aborted {
		t.Fatal("campaign not aborted")
	}
	if err := c.Report(20, []byte("b"), 2, 6, true); err != nil {
		t.Fatalf("Report b: %v", err)
	}
	if d := dev(t, c, "b"); d.state != Done || d.ver != 6 {
		t.Fatalf("b = %+v, want Done ver=6", d)
	}
}

// TestMandatoryVersionExact: a device exactly on a mandatory version does
// not repeat it; the hop is the next strictly greater mandatory (or T).
func TestMandatoryVersionExact(t *testing.T) {
	c := mustNew(t, 6, []int{3, 5}, 3, 1, 1, 100, 5)
	mustAdd(t, c, 0, "a", 3)
	mustAdd(t, c, 0, "b", 5)
	mustAdd(t, c, 0, "e", 1)
	items, err := c.Dispatch(0, 3)
	wantItems(t, items, err, []Item{
		{[]byte("a"), 5, 1},
		{[]byte("b"), 6, 2},
		{[]byte("e"), 3, 3},
	})
}

// TestDeadlineEqualTimeout: now == dl already settles the timeout.
func TestDeadlineEqualTimeout(t *testing.T) {
	c := mustNew(t, 9, nil, 1, 2, 10, 100, 5)
	mustAdd(t, c, 0, "a", 1)
	items, err := c.Dispatch(0, 1)
	wantItems(t, items, err, []Item{{[]byte("a"), 9, 1}})

	mustAdd(t, c, 99, "b", 1)
	if got := dev(t, c, "a").state; got != InFlight {
		t.Fatalf("a state at 99 = %v, want InFlight (dl=100 not reached)", got)
	}
	mustAdd(t, c, 100, "c", 1)
	if d := dev(t, c, "a"); d.state != Pending || d.attempts != 1 || d.readyAt != 110 {
		t.Fatalf("a = %+v, want Pending attempts=1 readyAt=110 (timeout at dl=100)", d)
	}
}

// TestTimeoutUsesDeadline: the failure time of a timeout is dl, not the
// calling now, so readyAt = dl + B*attempts.
func TestTimeoutUsesDeadline(t *testing.T) {
	c := mustNew(t, 9, nil, 1, 3, 10, 100, 5)
	mustAdd(t, c, 0, "a", 1)
	if _, err := c.Dispatch(0, 1); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	mustAdd(t, c, 500, "b", 1)
	if d := dev(t, c, "a"); d.state != Pending || d.attempts != 1 || d.readyAt != 110 {
		t.Fatalf("a = %+v, want Pending attempts=1 readyAt=110 (=dl+B, not now+B)", d)
	}
}

// TestStaleToken: after a timeout and re-dispatch the old token is stale.
func TestStaleToken(t *testing.T) {
	c := mustNew(t, 9, nil, 1, 3, 10, 100, 5)
	mustAdd(t, c, 0, "a", 1)
	items, err := c.Dispatch(0, 1)
	wantItems(t, items, err, []Item{{[]byte("a"), 9, 1}})
	mustAdd(t, c, 200, "b", 1) // settles a's timeout at dl=100
	items, err = c.Dispatch(200, 1)
	wantItems(t, items, err, []Item{{[]byte("a"), 9, 2}})
	if err := c.Report(200, []byte("a"), 1, 9, true); !errors.Is(err, ErrStale) {
		t.Fatalf("Report old tok = %v, want ErrStale", err)
	}
	if err := c.Report(200, []byte("a"), 2, 9, true); err != nil {
		t.Fatalf("Report current tok: %v", err)
	}
	if got := dev(t, c, "a").state; got != Done {
		t.Fatalf("a state = %v, want Done", got)
	}
}

// TestRejectionOrder checks the first-error-wins ordering and that
// rejected operations change no state.
func TestRejectionOrder(t *testing.T) {
	c := mustNew(t, 6, []int{3}, 2, 2, 10, 100, 5)
	mustAdd(t, c, 0, "a", 1)
	mustAdd(t, c, 0, "b", 1)
	items, err := c.Dispatch(0, 2)
	wantItems(t, items, err, []Item{{[]byte("a"), 3, 1}, {[]byte("b"), 3, 2}})
	mustAdd(t, c, 10, "z", 1) // maxNow = 10

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"invalid beats clockback", func() error { return c.Report(-1, nil, 0, 0, false) }, ErrInvalid},
		{"clockback", func() error { return c.Report(5, []byte("a"), 1, 3, true) }, ErrClockBack},
		{"unknown", func() error { return c.Report(10, []byte("q"), 1, 3, true) }, ErrUnknown},
		{"not-inflight beats stale", func() error { return c.Report(10, []byte("z"), 99, 3, true) }, ErrNotInFlight},
		{"stale", func() error { return c.Report(10, []byte("a"), 99, 3, true) }, ErrStale},
		{"version", func() error { return c.Report(10, []byte("a"), 1, 5, true) }, ErrVersion},
		{"exists", func() error { return c.AddDevice(10, []byte("a"), 1) }, ErrExists},
		{"invalid beats exists", func() error { return c.AddDevice(10, []byte("a"), 0) }, ErrInvalid},
		{"invalid dispatch n", func() error { _, err := c.Dispatch(10, 0); return err }, ErrInvalid},
		{"invalid dispatch n beats clockback", func() error { _, err := c.Dispatch(5, 0); return err }, ErrInvalid},
		{"clockback dispatch", func() error { _, err := c.Dispatch(5, 1); return err }, ErrClockBack},
		{"invalid now", func() error { return c.AddDevice(1_000_000_000_001, []byte("n"), 1) }, ErrInvalid},
	}
	for _, tc := range cases {
		if got := tc.run(); !errors.Is(got, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	// No rejected operation advanced the clock or changed state.
	if c.maxNow != 10 {
		t.Fatalf("maxNow = %d, want 10", c.maxNow)
	}
	if d := dev(t, c, "a"); d.state != InFlight || d.tok != 1 || d.ver != 1 {
		t.Fatalf("a = %+v, want untouched InFlight tok=1 ver=1", d)
	}
	if got := c.slots.Used(); got != 2 {
		t.Fatalf("slots used = %d, want 2", got)
	}

	// ok=false ignores ver entirely.
	if err := c.Report(10, []byte("a"), 1, 999, false); err != nil {
		t.Fatalf("Report failure: %v", err)
	}
	if d := dev(t, c, "a"); d.state != Pending || d.attempts != 1 || d.readyAt != 20 {
		t.Fatalf("a = %+v, want Pending attempts=1 readyAt=20", d)
	}
}

// TestPoppedBounds proves the ready/settle structures pop at most
// (actual + 1) elements per operation, independent of the device total:
// the same dispatch of 3 devices pops identically for 100 and 10000
// devices.
func TestPoppedBounds(t *testing.T) {
	type result struct {
		popReady  uint64
		popSettle uint64
	}
	results := map[int]result{}
	for _, total := range []int{100, 10000} {
		c := mustNew(t, 50, nil, 5, 2, 10, 100, 100_000)
		for i := 0; i < total; i++ {
			mustAdd(t, c, 0, fmt.Sprintf("d%05d", i), 1)
		}
		items, err := c.Dispatch(0, 3)
		if err != nil || len(items) != 3 {
			t.Fatalf("total=%d Dispatch: %v items=%d", total, err, len(items))
		}
		if got := c.popReady; got > uint64(len(items))+1 {
			t.Fatalf("total=%d popReady=%d > dispatched+1=%d", total, got, len(items)+1)
		}
		if _, err := c.Dispatch(0, 2); err != nil { // fill remaining slots, dl=100
			t.Fatalf("total=%d Dispatch2: %v", total, err)
		}
		settleBefore := c.popSettle
		if _, err := c.Dispatch(200, 1); err != nil { // settles 5 timeouts
			t.Fatalf("total=%d Dispatch3: %v", total, err)
		}
		settled := c.popSettle - settleBefore
		if settled != 5 {
			t.Fatalf("total=%d settle pops = %d, want 5 (one per timeout)", total, settled)
		}
		if settled > 5+1 {
			t.Fatalf("total=%d settle pops = %d > timeouts+1", total, settled)
		}
		results[total] = result{popReady: c.popReady, popSettle: c.popSettle}
	}
	if results[100] != results[10000] {
		t.Fatalf("pop counters differ by device total: %v vs %v", results[100], results[10000])
	}
}

// TestConcurrent hammers the campaign from goroutines; with -race this
// checks the mutex, and the token stream must stay contiguous from 1.
func TestConcurrent(t *testing.T) {
	c := mustNew(t, 100, nil, 10, 2, 5, 50, 1000)
	const devices = 100
	for i := 0; i < devices; i++ {
		mustAdd(t, c, 0, fmt.Sprintf("d%03d", i), 1)
	}
	var mu sync.Mutex
	seen := map[uint64]bool{}
	var maxTok uint64
	work := make(chan Item, devices)
	var wgD, wgR sync.WaitGroup
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		wgD.Add(1)
		go func() { // dispatchers
			defer wgD.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				items, err := c.Dispatch(0, 5)
				if err != nil {
					continue
				}
				mu.Lock()
				for _, it := range items {
					if seen[it.Tok] {
						t.Errorf("duplicate token %d", it.Tok)
					}
					seen[it.Tok] = true
					if it.Tok > maxTok {
						maxTok = it.Tok
					}
					work <- it
				}
				mu.Unlock()
			}
		}()
	}
	for g := 0; g < 4; g++ {
		wgR.Add(1)
		go func() { // reporters
			defer wgR.Done()
			for it := range work {
				_ = c.Report(0, it.ID, it.Tok, it.Hop, true)
			}
		}()
	}
	// Wait until every device is Done, then close the work channel.
	go func() {
		for {
			c.mu.Lock()
			n := 0
			for _, d := range c.devices {
				if d.state == Done {
					n++
				}
			}
			c.mu.Unlock()
			if n == devices {
				close(done)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	<-done
	wgD.Wait()  // dispatchers finish their fixed loops
	close(work) // reporters drain and exit
	wgR.Wait()
	for i := uint64(1); i <= maxTok; i++ {
		if !seen[i] {
			t.Fatalf("token %d missing: stream not contiguous", i)
		}
	}
	if got := c.slots.Used(); got != 0 {
		t.Fatalf("slots used = %d, want 0", got)
	}
}
