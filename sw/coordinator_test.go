package sw

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func at(sec int64) time.Time { return time.Unix(sec, 0) }

func newTestCoordinator() *Coordinator {
	return NewCoordinator(Config{MinUpdateInterval: 10 * time.Second})
}

func mustRegister(t *testing.T, c *Coordinator, scope, script, content string, now time.Time) *Registration {
	t.Helper()
	r, err := c.Register(scope, script, content, now)
	if err != nil {
		t.Fatalf("Register(%q) failed: %v", scope, err)
	}
	t.Logf("Register scope=%q script=%q -> ok (basis: scope valid, no conflicting registration)", scope, script)
	return r
}

func mustUpdate(t *testing.T, c *Coordinator, scope, content string, now time.Time) bool {
	t.Helper()
	created, err := c.CheckUpdate(scope, content, now)
	if err != nil {
		t.Fatalf("CheckUpdate(%q) failed: %v", scope, err)
	}
	t.Logf("CheckUpdate scope=%q -> created=%v (basis: digest compare with active/waiting/installing)", scope, created)
	return created
}

func mustInstallOK(t *testing.T, c *Coordinator, scope string, id uint64, opts InstallOpts, now time.Time) {
	t.Helper()
	if err := c.InstallSucceeded(scope, id, opts, now); err != nil {
		t.Fatalf("InstallSucceeded(scope=%q v=%d) failed: %v", scope, id, err)
	}
	t.Logf("InstallSucceeded scope=%q version=%d opts=%+v -> ok", scope, id, opts)
}

func mustNavigate(t *testing.T, c *Coordinator, clientID, url string, now time.Time) {
	t.Helper()
	if err := c.Navigate(clientID, url, now); err != nil {
		t.Fatalf("Navigate(%q, %q) failed: %v", clientID, url, err)
	}
}

func mustAway(t *testing.T, c *Coordinator, clientID string, now time.Time) {
	t.Helper()
	if err := c.NavigateAway(clientID, now); err != nil {
		t.Fatalf("NavigateAway(%q) failed: %v", clientID, err)
	}
}

func controlledBy(t *testing.T, c *Coordinator, clientID string) (uint64, bool) {
	t.Helper()
	cl := c.clients[clientID]
	if cl == nil || cl.controlledBy == nil {
		return 0, false
	}
	return cl.controlledBy.id, true
}

func assertControlled(t *testing.T, c *Coordinator, clientID string, want uint64) {
	t.Helper()
	got, ok := controlledBy(t, c, clientID)
	if !ok || got != want {
		t.Fatalf("client %q: controlled by version %d (ok=%v), want version %d", clientID, got, ok, want)
	}
	t.Logf("client %q controlled by version %d as expected", clientID, want)
}

func assertUncontrolled(t *testing.T, c *Coordinator, clientID string) {
	t.Helper()
	if got, ok := controlledBy(t, c, clientID); ok {
		t.Fatalf("client %q: controlled by version %d, want uncontrolled", clientID, got)
	}
	t.Logf("client %q uncontrolled as expected", clientID)
}

func TestNestedScopeLongestMatch(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "root.js", "root-v1", at(1))
	mustRegister(t, c, "/a/", "a.js", "a-v1", at(2))
	mustRegister(t, c, "/a/b/", "ab.js", "ab-v1", at(3))
	mustUpdate(t, c, "/", "root-v1", at(4))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(5))
	mustUpdate(t, c, "/a/", "a-v1", at(6))
	mustInstallOK(t, c, "/a/", 2, InstallOpts{}, at(7))
	mustUpdate(t, c, "/a/b/", "ab-v1", at(8))
	mustInstallOK(t, c, "/a/b/", 3, InstallOpts{}, at(9))

	mustNavigate(t, c, "c1", "/a/b/page", at(10))
	assertControlled(t, c, "c1", 3)
	t.Log("basis: /a/b/ is the longest registered prefix of /a/b/page")

	mustNavigate(t, c, "c2", "/a/other", at(11))
	assertControlled(t, c, "c2", 2)
	t.Log("basis: /a/b/ does not prefix /a/other; /a/ is the longest match")

	mustNavigate(t, c, "c3", "/z", at(12))
	assertControlled(t, c, "c3", 1)
	t.Log("basis: only / prefixes /z")

	mustNavigate(t, c, "c4", "/a", at(13))
	assertControlled(t, c, "c4", 1)
	t.Log("basis: /a/ is not a string prefix of /a, so only / matches")
}

func TestDuplicateRegisterSameScriptNoop(t *testing.T) {
	c := newTestCoordinator()
	r1 := mustRegister(t, c, "/app/", "sw.js", "v1", at(1))
	r2, err := c.Register("/app/", "sw.js", "v2-different-content", at(2))
	if err != nil {
		t.Fatalf("duplicate register failed: %v", err)
	}
	if r1 != r2 {
		t.Fatalf("duplicate register returned a different registration")
	}
	if len(r1.versions) != 0 {
		t.Fatalf("no-op register created versions: %d", len(r1.versions))
	}
	t.Log("basis: same scope + same scriptURL -> no-op, content digest is not even checked")
}

func TestSameDigestProducesNoVersion(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "same", at(1))
	if !mustUpdate(t, c, "/", "same", at(2)) {
		t.Fatal("first check should create a version")
	}
	created := mustUpdate(t, c, "/", "same", at(20))
	if created {
		t.Fatal("identical content must not create a new version")
	}
	if n := len(c.regs["/"].versions); n != 1 {
		t.Fatalf("versions = %d, want 1", n)
	}
	t.Log("basis: digest of new content equals current installing version digest -> no update")
}

func TestWaitingSlotDisplacedByNewerVersion(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	mustNavigate(t, c, "c1", "/x", at(4))
	assertControlled(t, c, "c1", 1)

	mustUpdate(t, c, "/", "v2", at(20))
	mustInstallOK(t, c, "/", 2, InstallOpts{}, at(21))
	if c.regs["/"].waiting == nil || c.regs["/"].waiting.id != 2 {
		t.Fatal("v2 should occupy the waiting slot while v1 controls a client")
	}

	mustUpdate(t, c, "/", "v3", at(40))
	mustInstallOK(t, c, "/", 3, InstallOpts{}, at(41))
	r := c.regs["/"]
	if r.waiting == nil || r.waiting.id != 3 {
		t.Fatal("v3 should displace v2 in the waiting slot")
	}
	if c.versions[2].state != StateRedundant {
		t.Fatalf("displaced v2 state = %v, want redundant", c.versions[2].state)
	}
	t.Log("basis: a newer successfully installed version evicts the previous waiting version")
}

func TestTakeoverWhenActiveClientsDrain(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	mustNavigate(t, c, "c1", "/x", at(4))
	mustUpdate(t, c, "/", "v2", at(20))
	mustInstallOK(t, c, "/", 2, InstallOpts{}, at(21))
	if c.regs["/"].active.id != 1 {
		t.Fatal("v1 must stay active while it controls a client")
	}
	mustAway(t, c, "c1", at(22))
	r := c.regs["/"]
	if r.active == nil || r.active.id != 2 {
		t.Fatal("v2 must take over once the active version's client count hits zero")
	}
	if c.versions[1].state != StateRedundant {
		t.Fatalf("old active v1 state = %v, want redundant", c.versions[1].state)
	}
	t.Log("basis: last controlled client leaving triggers waiting -> active takeover")
}

// setupTwoGenerations builds: v1 active, c0 controlled by v1; v2 skips waiting
// and takes over (v1 redundant, c0 stays on v1); c1 controlled by v2.
func setupTwoGenerations(t *testing.T) *Coordinator {
	t.Helper()
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	mustNavigate(t, c, "c0", "/x", at(4))
	mustUpdate(t, c, "/", "v2", at(20))
	mustInstallOK(t, c, "/", 2, InstallOpts{SkipWaiting: true}, at(21))
	assertControlled(t, c, "c0", 1)
	mustNavigate(t, c, "c1", "/y", at(22))
	assertControlled(t, c, "c1", 2)
	return c
}

func TestSkipWaitingAndClaimClientsCombinations(t *testing.T) {
	t.Run("neither", func(t *testing.T) {
		c := setupTwoGenerations(t)
		mustUpdate(t, c, "/", "v3", at(40))
		mustInstallOK(t, c, "/", 3, InstallOpts{}, at(41))
		if c.regs["/"].active.id != 2 {
			t.Fatal("v2 must stay active: no skip-waiting and it still controls c1")
		}
		mustAway(t, c, "c1", at(42))
		if c.regs["/"].active.id != 3 {
			t.Fatal("v3 takes over after v2's clients drain")
		}
		assertControlled(t, c, "c0", 1)
		t.Log("basis: no claim -> c0 keeps its redundant v1 for the rest of its page life")
	})

	t.Run("claim-only", func(t *testing.T) {
		c := setupTwoGenerations(t)
		mustUpdate(t, c, "/", "v3", at(40))
		mustInstallOK(t, c, "/", 3, InstallOpts{ClaimClients: true}, at(41))
		if c.regs["/"].active.id != 2 {
			t.Fatal("claim without skip-waiting must still wait for v2's clients to drain")
		}
		mustAway(t, c, "c1", at(42))
		if c.regs["/"].active.id != 3 {
			t.Fatal("v3 takes over once v2's client count reaches zero")
		}
		assertControlled(t, c, "c0", 3)
		t.Log("basis: claim at takeover transfers even clients stuck on the older redundant v1")
	})

	t.Run("skip-only", func(t *testing.T) {
		c := setupTwoGenerations(t)
		mustUpdate(t, c, "/", "v3", at(40))
		mustInstallOK(t, c, "/", 3, InstallOpts{SkipWaiting: true}, at(41))
		if c.regs["/"].active.id != 3 {
			t.Fatal("skip-waiting takes over immediately")
		}
		assertControlled(t, c, "c0", 1)
		assertControlled(t, c, "c1", 2)
		t.Log("basis: no claim -> existing clients keep their now-redundant versions")
	})

	t.Run("skip-and-claim", func(t *testing.T) {
		c := setupTwoGenerations(t)
		mustUpdate(t, c, "/", "v3", at(40))
		mustInstallOK(t, c, "/", 3, InstallOpts{SkipWaiting: true, ClaimClients: true}, at(41))
		if c.regs["/"].active.id != 3 {
			t.Fatal("skip-waiting takes over immediately")
		}
		assertControlled(t, c, "c0", 3)
		assertControlled(t, c, "c1", 3)
		t.Log("basis: claim transfers every client of the registration to the new active version")
	})
}

func TestControlledVersionStableDuringPageLife(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	mustNavigate(t, c, "c1", "/x", at(4))
	mustUpdate(t, c, "/", "v2", at(20))
	mustInstallOK(t, c, "/", 2, InstallOpts{SkipWaiting: true}, at(21))
	if c.regs["/"].active.id != 2 {
		t.Fatal("v2 must be active after skip-waiting")
	}
	assertControlled(t, c, "c1", 1)
	t.Log("basis: takeover without claim does not retarget existing clients")

	mustNavigate(t, c, "c1", "/x", at(22))
	assertControlled(t, c, "c1", 2)
	t.Log("basis: only a new navigation re-binds the client to the current active version")
}

func TestManifestInheritanceOnlyAtTakeover(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	if err := c.PutManifest("/", 1, "/res/a", "digest-a", at(4)); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	mustNavigate(t, c, "c0", "/x", at(5))

	mustUpdate(t, c, "/", "v2", at(20))
	mustInstallOK(t, c, "/", 2, InstallOpts{InheritManifest: true}, at(21))
	if err := c.PutManifest("/", 1, "/res/b", "digest-b", at(22)); err != nil {
		t.Fatalf("PutManifest on still-active v1: %v", err)
	}
	if err := c.PutManifest("/", 2, "/res/b", "own-b", at(23)); err != nil {
		t.Fatalf("PutManifest on waiting v2: %v", err)
	}
	if _, ok := c.versions[2].ManifestEntry("/res/a"); ok {
		t.Fatal("inheritance must not happen before takeover")
	}
	t.Log("basis: while v2 waits, v1's manifest changes are invisible to v2")

	mustAway(t, c, "c0", at(24))
	if c.regs["/"].active.id != 2 {
		t.Fatal("v2 should have taken over")
	}
	m := c.versions[2].Manifest()
	if m["/res/a"] != "digest-a" || m["/res/b"] != "own-b" || len(m) != 2 {
		t.Fatalf("inherited manifest = %v, want a inherited and own b kept", m)
	}
	t.Log("basis: inheritance is a one-shot copy at takeover; v2's own entries win")

	if err := c.PutManifest("/", 1, "/res/c", "digest-c", at(25)); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("PutManifest on redundant v1 = %v, want ErrStateNotAllowed", err)
	}
	if _, ok := c.versions[2].ManifestEntry("/res/c"); ok {
		t.Fatal("post-takeover changes to the old manifest must not leak into v2")
	}

	mustNavigate(t, c, "c1", "/y", at(26))
	ans, err := c.Request("c1", "/res/a")
	if err != nil || ans.Source != SourceCache || ans.Digest != "digest-a" {
		t.Fatalf("Request(/res/a) = %+v, %v; want cache hit digest-a", ans, err)
	}
	ans, _ = c.Request("c1", "/res/missing")
	if ans.Source != SourceNetwork {
		t.Fatalf("Request(/res/missing) = %+v, want network", ans)
	}
	ans, _ = c.Request("c0", "/res/a")
	if ans.Source != SourceNetwork {
		t.Fatalf("uncontrolled client got %+v, want network", ans)
	}
	t.Log("basis: hits serve the version's digest, misses and uncontrolled clients fall back to network")
}

func TestRedundantManifestUnreadable(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	if err := c.PutManifest("/", 1, "/res/a", "digest-a", at(4)); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	mustNavigate(t, c, "c0", "/x", at(5))
	mustUpdate(t, c, "/", "v2", at(20))
	mustInstallOK(t, c, "/", 2, InstallOpts{SkipWaiting: true}, at(21))
	assertControlled(t, c, "c0", 1)
	ans, _ := c.Request("c0", "/res/a")
	if ans.Source != SourceNetwork {
		t.Fatalf("client of redundant version got %+v, want network", ans)
	}
	t.Log("basis: a redundant version's manifest is unreadable even for its own clients")
}

func TestUnregisterFinalizesAfterLastClientLeaves(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	mustNavigate(t, c, "c0", "/x", at(4))
	mustNavigate(t, c, "c1", "/y", at(5))

	if err := c.Unregister("/", at(6)); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if !c.regs["/"].pendingRemoval {
		t.Fatal("registration must be pending removal while clients remain")
	}
	if _, err := c.CheckUpdate("/", "v2", at(20)); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("CheckUpdate during pending removal = %v, want ErrStateNotAllowed", err)
	}
	mustNavigate(t, c, "c2", "/z", at(21))
	assertUncontrolled(t, c, "c2")
	t.Log("basis: pending-removal registrations accept no new clients and no update checks")

	mustAway(t, c, "c0", at(22))
	if c.regs["/"] == nil {
		t.Fatal("registration must survive until the last controlled client leaves")
	}
	assertControlled(t, c, "c1", 1)

	mustAway(t, c, "c1", at(23))
	if c.regs["/"] != nil {
		t.Fatal("registration must be finalized once the last controlled client leaves")
	}
	if c.versions[1].state != StateRedundant {
		t.Fatalf("finalized version state = %v, want redundant", c.versions[1].state)
	}
	t.Log("basis: last client leaving finalizes the registration and redundants all versions")
}

func TestReregisterDuringPendingRemovalRevives(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "v1", at(1))
	mustUpdate(t, c, "/", "v1", at(2))
	mustInstallOK(t, c, "/", 1, InstallOpts{}, at(3))
	mustNavigate(t, c, "c0", "/x", at(4))
	if err := c.Unregister("/", at(5)); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	r, err := c.Register("/", "sw.js", "v1", at(6))
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if r.pendingRemoval {
		t.Fatal("re-register must cancel pending removal")
	}
	if r.active == nil || r.active.id != 1 {
		t.Fatal("version relations must be preserved across revival")
	}
	assertControlled(t, c, "c0", 1)
	mustNavigate(t, c, "c1", "/y", at(7))
	assertControlled(t, c, "c1", 1)
	t.Log("basis: revival restores client assignment and keeps the original versions")
}

func TestMinUpdateIntervalBoundary(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "a", at(0))
	mustUpdate(t, c, "/", "a", at(1))

	if _, err := c.CheckUpdate("/", "b", at(10)); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("check at +9s = %v, want ErrTooFrequent", err)
	}
	t.Log("basis: 9s < 10s minimum interval -> too frequent")

	if !mustUpdate(t, c, "/", "b", at(11)) {
		t.Fatal("check at exactly +10s must be allowed")
	}
	t.Log("basis: exactly at the minimum interval boundary the check is allowed")

	if _, err := c.CheckUpdate("/", "c", at(20)); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("check at +9s after second check = %v, want ErrTooFrequent", err)
	}
	mustUpdate(t, c, "/", "c", at(21))

	r, err := c.Register("/", "sw-v2.js", "d", at(22))
	if err != nil {
		t.Fatalf("script-change register: %v", err)
	}
	if n := len(r.versions); n != 4 {
		t.Fatalf("versions = %d, want 4 (script-change check bypasses the interval)", n)
	}
	t.Log("basis: a check triggered by a scriptURL change ignores the minimum interval")
}

func TestErrorPrecedence(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "a", at(1))
	mustUpdate(t, c, "/", "a", at(10))
	mustUpdate(t, c, "/", "b", at(20))

	type op struct {
		name string
		run  func() error
		want error
	}
	ops := []op{
		{"invalid-arg beats clock-rollback", func() error {
			_, err := c.CheckUpdate("no-slash", "x", at(5))
			return err
		}, ErrInvalidArgument},
		{"clock-rollback beats registration-not-found", func() error {
			_, err := c.CheckUpdate("/missing/", "x", at(5))
			return err
		}, ErrClockRollback},
		{"registration-not-found", func() error {
			_, err := c.CheckUpdate("/missing/", "x", at(21))
			return err
		}, ErrRegistrationNotFound},
		{"version-not-found", func() error {
			return c.InstallSucceeded("/", 999, InstallOpts{}, at(22))
		}, ErrVersionNotFound},
		{"state-not-allowed", func() error {
			return c.InstallSucceeded("/", 1, InstallOpts{}, at(23))
		}, ErrStateNotAllowed},
		{"too-frequent", func() error {
			_, err := c.CheckUpdate("/", "c", at(25))
			return err
		}, ErrTooFrequent},
	}
	for _, o := range ops {
		before := len(c.regs["/"].versions)
		lastCheck := c.regs["/"].lastCheck
		lastTime := c.lastTime
		err := o.run()
		if !errors.Is(err, o.want) {
			t.Fatalf("%s: got %v, want %v", o.name, err, o.want)
		}
		if len(c.regs["/"].versions) != before || !c.regs["/"].lastCheck.Equal(lastCheck) || !c.lastTime.Equal(lastTime) {
			t.Fatalf("%s: rejected op mutated state", o.name)
		}
		t.Logf("%s -> %v (state unchanged)", o.name, o.want)
	}
}

func TestRedundantVersionRejectsOps(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "a", at(1))
	mustUpdate(t, c, "/", "a", at(2))
	mustUpdate(t, c, "/", "b", at(20))

	if err := c.InstallSucceeded("/", 1, InstallOpts{}, at(21)); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("InstallSucceeded on displaced v1 = %v", err)
	}
	if err := c.InstallFailed("/", 1, at(22)); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("InstallFailed on redundant v1 = %v", err)
	}
	if err := c.SkipWaiting("/", 1, at(23)); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("SkipWaiting on redundant v1 = %v", err)
	}
	if err := c.PutManifest("/", 1, "/r", "d", at(24)); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("PutManifest on redundant v1 = %v", err)
	}
	t.Log("basis: redundant is terminal; every version op on it is rejected")
}

func TestClockRollback(t *testing.T) {
	c := newTestCoordinator()
	mustRegister(t, c, "/", "sw.js", "a", at(10))
	if err := c.Navigate("c1", "/x", at(9)); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Navigate with rolled-back clock = %v", err)
	}
	if err := c.NavigateAway("c1", at(9)); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("NavigateAway with rolled-back clock = %v", err)
	}
	if err := c.Unregister("/", at(9)); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Unregister with rolled-back clock = %v", err)
	}
	if _, ok := controlledBy(t, c, "c1"); ok {
		t.Fatal("rejected navigate must not control the client")
	}
	if c.regs["/"] == nil {
		t.Fatal("rejected unregister must not remove the registration")
	}
	if err := c.Navigate("c1", "/x", at(10)); err != nil {
		t.Fatalf("equal timestamp must be accepted: %v", err)
	}
	t.Log("basis: now < last accepted time is rejected; equal is fine; rejections mutate nothing")
}

func checkInvariants(t *testing.T, c *Coordinator) {
	t.Helper()
	for scope, r := range c.regs {
		counts := map[VersionState]int{}
		for _, v := range r.versions {
			counts[v.state]++
		}
		for _, s := range []VersionState{StateInstalling, StateWaiting, StateActive} {
			if counts[s] > 1 {
				t.Fatalf("reg %q holds %d versions in state %v", scope, counts[s], s)
			}
		}
		n := 0
		for _, v := range r.versions {
			n += len(v.clients)
		}
		if n != r.controlled {
			t.Fatalf("reg %q controlled=%d but versions hold %d clients", scope, r.controlled, n)
		}
	}
	for id, cl := range c.clients {
		if cl.controlledBy == nil {
			continue
		}
		s := cl.controlledBy.state
		if s != StateActive && s != StateRedundant {
			t.Fatalf("client %q controlled by version in state %v", id, s)
		}
		if _, ok := cl.controlledBy.clients[id]; !ok {
			t.Fatalf("client %q missing from its version's client set", id)
		}
	}
}

func TestConcurrentOpsSerialize(t *testing.T) {
	c := newTestCoordinator()
	var clock atomic.Int64
	next := func() time.Time { return at(clock.Add(1)) }
	scopes := []string{"/", "/a/", "/a/b/", "/b/"}
	contents := []string{"k1", "k2", "k3"}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				scope := scopes[(g+i)%len(scopes)]
				client := fmt.Sprintf("c%d", (g+i)%6)
				switch (g + i) % 6 {
				case 0:
					c.Register(scope, "sw.js", contents[i%len(contents)], next())
				case 1:
					c.CheckUpdate(scope, contents[(i+1)%len(contents)], next())
				case 2:
					c.Navigate(client, scope+"page", next())
				case 3:
					c.NavigateAway(client, next())
				case 4:
					c.Request(client, "/res/x")
				case 5:
					c.mu.Lock()
					var id uint64
					ok := false
					if r := c.regs[scope]; r != nil && r.installing != nil {
						id, ok = r.installing.id, true
					}
					c.mu.Unlock()
					if ok {
						c.InstallSucceeded(scope, id, InstallOpts{SkipWaiting: i%2 == 0, ClaimClients: i%3 == 0}, next())
					}
				}
			}
		}(g)
	}
	wg.Wait()
	checkInvariants(t, c)
	t.Log("basis: a single mutex serializes all ops; slot and control invariants hold afterwards")
}
