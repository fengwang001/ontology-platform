package vrrp

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func mustDevice(t *testing.T, cfg Config) *Device {
	t.Helper()
	d, err := NewDevice(cfg)
	if err != nil {
		t.Fatalf("NewDevice(%+v): %v", cfg, err)
	}
	return d
}

func mustHandle(t *testing.T, d *Device, ev Event) Result {
	t.Helper()
	res, err := d.Handle(ev)
	if err != nil {
		t.Fatalf("Handle(%s): unexpected error: %v", ev, err)
	}
	return res
}

// mustReject expects the event to be rejected with the given error kind
// and verifies that state, timers and clock are untouched.
func mustReject(t *testing.T, d *Device, ev Event, kind ErrKind) {
	t.Helper()
	before := d.Snapshot()
	res, err := d.Handle(ev)
	if err == nil {
		t.Fatalf("Handle(%s): expected %v, got success %+v", ev, kind, res)
	}
	var verr *Error
	if !errors.As(err, &verr) {
		t.Fatalf("Handle(%s): error type %T is not *Error", ev, err)
	}
	if verr.Kind != kind {
		t.Fatalf("Handle(%s): expected %v, got %v", ev, kind, verr.Kind)
	}
	if after := d.Snapshot(); after != before {
		t.Fatalf("rejected event changed state: before %+v after %+v", before, after)
	}
}

func mustAdverts(t *testing.T, res Result, want ...Advert) {
	t.Helper()
	if !slices.Equal(res.Adverts, want) {
		t.Fatalf("adverts: got %v, want %v", res.Adverts, want)
	}
}

func testConfig() Config {
	return Config{ID: "self", Priority: 100, Preempt: true, AdvertIntervalMs: 100}
}

// Timeout at exactly 3x the sender interval promotes; one millisecond
// earlier does not.
func TestWatchTimeoutBoundary(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(10))
	res := mustHandle(t, d, AdvanceTime(10+300-1))
	mustAdverts(t, res)
	if got := d.Snapshot().Role; got != RoleBackup {
		t.Fatalf("at 3x-1ms: role = %v, want backup", got)
	}
	res = mustHandle(t, d, AdvanceTime(10+300))
	mustAdverts(t, res, Advert{SenderID: "self", Priority: 100, IntervalMs: 100})
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("at exactly 3x: role = %v, want master", got)
	}
}

// Same boundary, but measured from the last accepted advert with the
// sender's interval.
func TestWatchTimeoutBoundaryFromAdvert(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "other", Priority: 200, IntervalMs: 50}, 100))
	mustHandle(t, d, AdvanceTime(100+150-1))
	if got := d.Snapshot().Role; got != RoleBackup {
		t.Fatalf("at 3x-1ms: role = %v, want backup", got)
	}
	mustHandle(t, d, AdvanceTime(100+150))
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("at exactly 3x: role = %v, want master", got)
	}
}

// Equal priority: the sender identifier decides; equal identifier is a
// conflict.
func TestEqualPriorityIdentifierComparison(t *testing.T) {
	d := mustDevice(t, Config{ID: "m", Priority: 100, Preempt: true, AdvertIntervalMs: 100})
	mustHandle(t, d, Start(0))

	// Smaller id with equal priority is inferior: ignored under preempt.
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "a", Priority: 100, IntervalMs: 40}, 10))
	if got := d.Snapshot().WatchBase; got != 0 {
		t.Fatalf("inferior equal-priority advert reset watch base to %d", got)
	}

	// Larger id with equal priority is superior: accepted, watch reset.
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "z", Priority: 100, IntervalMs: 40}, 20))
	snap := d.Snapshot()
	if snap.WatchBase != 20 || snap.WatchInterval != 40 {
		t.Fatalf("superior equal-priority advert not adopted: %+v", snap)
	}

	// Identical id and priority is an identifier conflict.
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "m", Priority: 100, IntervalMs: 40}, 30),
		ErrIdentifierConflict)
}

// Lower priority adverts: ignored when preempt is on, adopted when off.
func TestPreemptSwitchLowerPriority(t *testing.T) {
	on := mustDevice(t, Config{ID: "self", Priority: 100, Preempt: true, AdvertIntervalMs: 100})
	mustHandle(t, on, Start(0))
	mustHandle(t, on, ReceiveAdvert(Advert{SenderID: "low", Priority: 50, IntervalMs: 40}, 10))
	if snap := on.Snapshot(); snap.WatchBase != 0 || snap.WatchInterval != 100 {
		t.Fatalf("preempt on: watch changed by inferior advert: %+v", snap)
	}

	off := mustDevice(t, Config{ID: "self", Priority: 100, Preempt: false, AdvertIntervalMs: 100})
	mustHandle(t, off, Start(0))
	mustHandle(t, off, ReceiveAdvert(Advert{SenderID: "low", Priority: 50, IntervalMs: 40}, 10))
	if snap := off.Snapshot(); snap.WatchBase != 10 || snap.WatchInterval != 40 {
		t.Fatalf("preempt off: inferior advert not adopted: %+v", snap)
	}
}

// A zero-priority advert arms a promotion wait of floor(interval/4); the
// boundary is exact, and an accepted advert cancels the wait.
func TestYieldWaitBoundaryAndCancel(t *testing.T) {
	// Promotion after exactly interval/4 ms.
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "master", Priority: 0, IntervalMs: 100}, 100))
	if snap := d.Snapshot(); !snap.SkewSet || snap.SkewDeadline != 125 {
		t.Fatalf("promotion wait not armed for 125: %+v", snap)
	}
	mustHandle(t, d, AdvanceTime(124))
	if got := d.Snapshot().Role; got != RoleBackup {
		t.Fatalf("before wait expiry: role = %v, want backup", got)
	}
	res := mustHandle(t, d, AdvanceTime(125))
	mustAdverts(t, res, Advert{SenderID: "self", Priority: 100, IntervalMs: 100})
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("at wait expiry: role = %v, want master", got)
	}

	// An accepted advert cancels the pending wait.
	c := mustDevice(t, testConfig())
	mustHandle(t, c, Start(0))
	mustHandle(t, c, ReceiveAdvert(Advert{SenderID: "master", Priority: 0, IntervalMs: 100}, 100))
	mustHandle(t, c, ReceiveAdvert(Advert{SenderID: "master", Priority: 200, IntervalMs: 100}, 110))
	if snap := c.Snapshot(); snap.SkewSet {
		t.Fatalf("accepted advert did not cancel the wait: %+v", snap)
	}
	mustHandle(t, c, AdvanceTime(125))
	if got := c.Snapshot().Role; got != RoleBackup {
		t.Fatalf("cancelled wait still fired: role = %v", got)
	}
	// The normal watch is still armed from the accepted advert.
	mustHandle(t, c, AdvanceTime(110+300-1))
	if got := c.Snapshot().Role; got != RoleBackup {
		t.Fatalf("at 3x-1ms: role = %v, want backup", got)
	}
	mustHandle(t, c, AdvanceTime(110+300))
	if got := c.Snapshot().Role; got != RoleMaster {
		t.Fatalf("at exactly 3x: role = %v, want master", got)
	}
}

// The wait uses integer floor division of a quarter of the interval.
func TestYieldWaitFloorDivision(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "master", Priority: 0, IntervalMs: 7}, 50))
	if snap := d.Snapshot(); snap.SkewDeadline != 51 {
		t.Fatalf("floor(7/4)=1: deadline = %d, want 51", snap.SkewDeadline)
	}
	mustHandle(t, d, AdvanceTime(51))
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("role = %v, want master", got)
	}
}

// Address owner: immediate master on start, ignores all non-255 adverts
// in master (including zero-priority probes), conflicts on 255, and its
// priority can neither be reached nor left at runtime.
func TestAddressOwnerBranches(t *testing.T) {
	d := mustDevice(t, Config{ID: "owner", Priority: 255, Preempt: true, AdvertIntervalMs: 100})
	res := mustHandle(t, d, Start(0))
	mustAdverts(t, res, Advert{SenderID: "owner", Priority: 255, IntervalMs: 100})
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("owner did not become master on start: %v", got)
	}

	// Zero-priority probe is ignored by the owner.
	res = mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "probe", Priority: 0, IntervalMs: 100}, 10))
	mustAdverts(t, res)
	// Higher non-255 priority is ignored (owner never demotes).
	res = mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "other", Priority: 254, IntervalMs: 100}, 20))
	mustAdverts(t, res)
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("owner demoted: %v", got)
	}
	// Another 255 is an address owner conflict.
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "other", Priority: 255, IntervalMs: 100}, 30),
		ErrAddressOwnerConflict)
	// The owner cannot leave 255 at runtime.
	mustReject(t, d, SetPriority(100, 40), ErrInvalidArgument)
	mustReject(t, d, SetPriority(255, 40), ErrInvalidArgument)

	// A non-owner cannot become owner at runtime.
	n := mustDevice(t, testConfig())
	mustReject(t, n, SetPriority(255, 0), ErrInvalidArgument)
	mustReject(t, n, SetPriority(0, 0), ErrInvalidArgument)
	mustReject(t, n, SetPriority(256, 0), ErrInvalidArgument)
}

// Rejection precedence: invalid argument > clock rollback > not started
// > conflict.
func TestRejectionOrder(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, AdvanceTime(100)) // accepted no-op, clock = 100

	// Invalid argument beats clock rollback.
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "x", Priority: 100, IntervalMs: 0}, 50),
		ErrInvalidArgument)
	mustReject(t, d, SetPriority(0, 50), ErrInvalidArgument)
	// Clock rollback beats not started.
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "x", Priority: 100, IntervalMs: 100}, 50),
		ErrClockRollback)
	mustReject(t, d, TakeAdvert(50), ErrClockRollback)
	// Not started beats a would-be identifier conflict.
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "self", Priority: 100, IntervalMs: 100}, 100),
		ErrNotStarted)
	mustReject(t, d, TakeAdvert(100), ErrNotStarted)
}

// Stop returns to init (masters emit a zero-priority advert) and a later
// start behaves like a fresh one.
func TestStopAndRestart(t *testing.T) {
	// Non-owner: backup -> init -> backup.
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	res := mustHandle(t, d, Stop(10))
	mustAdverts(t, res)
	if got := d.Snapshot().Role; got != RoleInit {
		t.Fatalf("after stop: role = %v, want init", got)
	}
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "x", Priority: 200, IntervalMs: 100}, 20),
		ErrNotStarted)
	mustHandle(t, d, Start(30))
	if snap := d.Snapshot(); snap.Role != RoleBackup || snap.WatchBase != 30 {
		t.Fatalf("restart did not re-arm watch: %+v", snap)
	}

	// Promoted master yields on stop, then restarts as backup.
	m := mustDevice(t, testConfig())
	mustHandle(t, m, Start(0))
	mustHandle(t, m, AdvanceTime(300))
	if got := m.Snapshot().Role; got != RoleMaster {
		t.Fatalf("role = %v, want master", got)
	}
	res = mustHandle(t, m, Stop(400))
	mustAdverts(t, res, Advert{SenderID: "self", Priority: 0, IntervalMs: 100})
	if got := m.Snapshot().Role; got != RoleInit {
		t.Fatalf("after stop: role = %v, want init", got)
	}
	mustHandle(t, m, Start(500))
	if got := m.Snapshot().Role; got != RoleBackup {
		t.Fatalf("after restart: role = %v, want backup", got)
	}

	// Owner: master -> init -> master again with a fresh advert.
	o := mustDevice(t, Config{ID: "owner", Priority: 255, Preempt: true, AdvertIntervalMs: 100})
	mustHandle(t, o, Start(0))
	res = mustHandle(t, o, Stop(10))
	mustAdverts(t, res, Advert{SenderID: "owner", Priority: 0, IntervalMs: 100})
	res = mustHandle(t, o, Start(20))
	mustAdverts(t, res, Advert{SenderID: "owner", Priority: 255, IntervalMs: 100})
	if got := o.Snapshot().Role; got != RoleMaster {
		t.Fatalf("owner restart: role = %v, want master", got)
	}
}

// Periodic adverts: due at last-send + interval, at most one per take,
// next deadline counted from the take time, no backlog catch-up.
func TestMasterPeriodicAdverts(t *testing.T) {
	d := mustDevice(t, Config{ID: "owner", Priority: 255, Preempt: true, AdvertIntervalMs: 100})
	mustHandle(t, d, Start(0)) // advert at 0, next due 100

	res := mustHandle(t, d, TakeAdvert(99))
	mustAdverts(t, res)
	res = mustHandle(t, d, TakeAdvert(100))
	mustAdverts(t, res, Advert{SenderID: "owner", Priority: 255, IntervalMs: 100})

	// Skip several periods: a single take at 500 emits exactly one
	// advert and the next deadline is 500+100, not 200.
	res = mustHandle(t, d, TakeAdvert(500))
	mustAdverts(t, res, Advert{SenderID: "owner", Priority: 255, IntervalMs: 100})
	res = mustHandle(t, d, TakeAdvert(599))
	mustAdverts(t, res)
	res = mustHandle(t, d, TakeAdvert(600))
	mustAdverts(t, res, Advert{SenderID: "owner", Priority: 255, IntervalMs: 100})

	// Time advance alone never emits adverts.
	res = mustHandle(t, d, AdvanceTime(5000))
	mustAdverts(t, res)
	res = mustHandle(t, d, TakeAdvert(5000))
	mustAdverts(t, res, Advert{SenderID: "owner", Priority: 255, IntervalMs: 100})
}

// A non-owner master replies to a zero-priority probe immediately and
// reschedules its periodic advert from the probe time.
func TestMasterProbeReply(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	mustHandle(t, d, AdvanceTime(300)) // promoted, advert at 300, next due 400
	res := mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "probe", Priority: 0, IntervalMs: 40}, 350))
	mustAdverts(t, res, Advert{SenderID: "self", Priority: 100, IntervalMs: 100})
	if got := d.Snapshot().NextAdvert; got != 450 {
		t.Fatalf("next advert = %d, want 450", got)
	}
}

// A master demotes on a superior advert and re-arms the watch with the
// sender's interval; inferior adverts are ignored.
func TestMasterDemotion(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	mustHandle(t, d, AdvanceTime(300))

	// Inferior priority ignored.
	res := mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "low", Priority: 50, IntervalMs: 40}, 310))
	mustAdverts(t, res)
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("demoted by inferior advert: %v", got)
	}
	// Equal priority, smaller id ignored.
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "aaa", Priority: 100, IntervalMs: 40}, 320))
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("demoted by equal-priority smaller-id advert: %v", got)
	}
	// Equal priority, larger id demotes.
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "zzz", Priority: 100, IntervalMs: 40}, 330))
	if snap := d.Snapshot(); snap.Role != RoleBackup || snap.WatchBase != 330 || snap.WatchInterval != 40 {
		t.Fatalf("not demoted by equal-priority larger-id advert: %+v", snap)
	}

	// Higher priority demotes as well.
	m2 := mustDevice(t, testConfig())
	mustHandle(t, m2, Start(0))
	mustHandle(t, m2, AdvanceTime(300))
	mustHandle(t, m2, ReceiveAdvert(Advert{SenderID: "hi", Priority: 200, IntervalMs: 40}, 310))
	if snap := m2.Snapshot(); snap.Role != RoleBackup || snap.WatchInterval != 40 {
		t.Fatalf("not demoted by higher priority: %+v", snap)
	}

	// Identifier conflict is reported in master too.
	m3 := mustDevice(t, testConfig())
	mustHandle(t, m3, Start(0))
	mustHandle(t, m3, AdvanceTime(300))
	mustReject(t, m3, ReceiveAdvert(Advert{SenderID: "self", Priority: 100, IntervalMs: 40}, 310),
		ErrIdentifierConflict)
}

// Priority changes affect only future adverts and comparisons, never the
// current role or timers.
func TestSetPrioritySemantics(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	mustHandle(t, d, SetPriority(200, 10))
	if snap := d.Snapshot(); snap.Role != RoleBackup || snap.WatchBase != 0 {
		t.Fatalf("set-priority changed role or timers: %+v", snap)
	}
	// The promotion advert carries the new priority.
	res := mustHandle(t, d, AdvanceTime(300))
	mustAdverts(t, res, Advert{SenderID: "self", Priority: 200, IntervalMs: 100})
	// And comparisons use it: priority 150 is now inferior.
	res = mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "mid", Priority: 150, IntervalMs: 40}, 310))
	mustAdverts(t, res)
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("demoted by now-inferior advert: %v", got)
	}
}

// When the promotion wait and the watch expire at the same instant, the
// promotion wait is consulted first (observable via the reason string).
func TestSimultaneousExpiryPrefersSkew(t *testing.T) {
	d := mustDevice(t, Config{ID: "self", Priority: 100, Preempt: true, AdvertIntervalMs: 100})
	mustHandle(t, d, Start(0))
	// Accepted advert at 100 with interval 100: watch deadline 400.
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "m", Priority: 200, IntervalMs: 100}, 100))
	// Zero-priority advert at 300 with interval 400: wait deadline 400.
	mustHandle(t, d, ReceiveAdvert(Advert{SenderID: "m", Priority: 0, IntervalMs: 400}, 300))
	res := mustHandle(t, d, AdvanceTime(400))
	mustAdverts(t, res, Advert{SenderID: "self", Priority: 100, IntervalMs: 100})
	if res.Reason == "" || !strings.Contains(res.Reason, "promotion wait") {
		t.Fatalf("expected promotion-wait reason, got %q", res.Reason)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []Config{
		{ID: "", Priority: 100, AdvertIntervalMs: 100},
		{ID: "x", Priority: 0, AdvertIntervalMs: 100},
		{ID: "x", Priority: 256, AdvertIntervalMs: 100},
		{ID: "x", Priority: 100, AdvertIntervalMs: 0},
		{ID: "x", Priority: 100, AdvertIntervalMs: MaxAdvertIntervalMs + 1},
	}
	for _, cfg := range cases {
		if _, err := NewDevice(cfg); err == nil {
			t.Fatalf("NewDevice(%+v): expected error", cfg)
		} else {
			var verr *Error
			if !errors.As(err, &verr) || verr.Kind != ErrInvalidArgument {
				t.Fatalf("NewDevice(%+v): expected invalid argument, got %v", cfg, err)
			}
		}
	}
	if _, err := NewDevice(Config{ID: "x", Priority: 255, AdvertIntervalMs: MaxAdvertIntervalMs}); err != nil {
		t.Fatalf("valid owner config rejected: %v", err)
	}
}

// Malformed received adverts are invalid arguments.
func TestReceiveAdvertValidation(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "", Priority: 100, IntervalMs: 100}, 10),
		ErrInvalidArgument)
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "x", Priority: -1, IntervalMs: 100}, 10),
		ErrInvalidArgument)
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "x", Priority: 256, IntervalMs: 100}, 10),
		ErrInvalidArgument)
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "x", Priority: 100, IntervalMs: 0}, 10),
		ErrInvalidArgument)
	mustReject(t, d, ReceiveAdvert(Advert{SenderID: "x", Priority: 100, IntervalMs: MaxAdvertIntervalMs + 1}, 10),
		ErrInvalidArgument)
}
