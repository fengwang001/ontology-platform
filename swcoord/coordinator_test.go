package swcoord

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// testRig bundles a coordinator with a controllable clock and script
// digests. Helpers log inputs, outputs and the facts each verdict
// relies on, as required by the test logging rules.
type testRig struct {
	t       *testing.T
	c       *Coordinator
	now     time.Time
	digests map[string]string
}

func newRig(t *testing.T, minInterval time.Duration) *testRig {
	t.Helper()
	rig := &testRig{
		t:       t,
		now:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		digests: make(map[string]string),
	}
	rig.c = New(Options{
		MinUpdateInterval: minInterval,
		Now:               func() time.Time { return rig.now },
		ScriptDigest:      func(url string) (string, error) { return rig.digests[url], nil },
	})
	return rig
}

func (r *testRig) advance(d time.Duration) {
	r.now = r.now.Add(d)
	r.t.Logf("clock += %s -> %s", d, r.now.Format(time.RFC3339))
}

func (r *testRig) mustRegister(scope, script string) {
	r.t.Helper()
	existed, err := r.c.Register(scope, script)
	r.t.Logf("Register(%q,%q) -> existed=%v err=%v", scope, script, existed, err)
	if err != nil {
		r.t.Fatalf("Register(%q,%q): %v", scope, script, err)
	}
}

func (r *testRig) mustUpdate(scope string) uint64 {
	r.t.Helper()
	id, created, err := r.c.CheckUpdate(scope)
	r.t.Logf("CheckUpdate(%q) -> id=%d created=%v err=%v", scope, id, created, err)
	if err != nil || !created {
		r.t.Fatalf("CheckUpdate(%q): id=%d created=%v err=%v", scope, id, created, err)
	}
	return id
}

func (r *testRig) mustInstall(id uint64, manifest map[string]string) {
	r.t.Helper()
	err := r.c.InstallSuccess(id, manifest)
	r.t.Logf("InstallSuccess(%d,%v) -> err=%v", id, manifest, err)
	if err != nil {
		r.t.Fatalf("InstallSuccess(%d): %v", id, err)
	}
}

func (r *testRig) mustNavigate(client, url string) {
	r.t.Helper()
	err := r.c.Navigate(client, url)
	r.t.Logf("Navigate(%q,%q) -> err=%v", client, url, err)
	if err != nil {
		r.t.Fatalf("Navigate(%q,%q): %v", client, url, err)
	}
}

func (r *testRig) mustUnload(client string) {
	r.t.Helper()
	if err := r.c.Unload(client); err != nil {
		r.t.Fatalf("Unload(%q): %v", client, err)
	}
	r.t.Logf("Unload(%q) -> ok", client)
}

func (r *testRig) wantState(id uint64, want VersionState) {
	r.t.Helper()
	info, ok := r.c.VersionInfo(id)
	if !ok {
		r.t.Fatalf("version %d missing", id)
	}
	r.t.Logf("version %d state=%s clients=%d (want %s)", id, info.State, info.ClientCount, want)
	if info.State != want {
		r.t.Fatalf("version %d state=%s, want %s", id, info.State, want)
	}
}

func (r *testRig) wantClient(client string, want uint64) {
	r.t.Helper()
	got, controlled := r.c.ClientInfo(client)
	r.t.Logf("client %q controlled-by=%d controlled=%v (want %d)", client, got, controlled, want)
	if want == 0 {
		if controlled {
			r.t.Fatalf("client %q unexpectedly controlled by %d", client, got)
		}
		return
	}
	if !controlled || got != want {
		r.t.Fatalf("client %q controlled by %d, want %d", client, got, want)
	}
}

func (r *testRig) wantFetch(client, url string, wantDigest string, wantNetwork bool) {
	r.t.Helper()
	res, err := r.c.Fetch(client, url)
	r.t.Logf("Fetch(%q,%q) -> %+v err=%v (want digest=%q network=%v)",
		client, url, res, err, wantDigest, wantNetwork)
	if err != nil {
		r.t.Fatalf("Fetch(%q,%q): %v", client, url, err)
	}
	if res.Network != wantNetwork || res.Digest != wantDigest {
		r.t.Fatalf("Fetch(%q,%q)=%+v, want digest=%q network=%v",
			client, url, res, wantDigest, wantNetwork)
	}
}

func (r *testRig) wantErr(op string, err error, want error) {
	r.t.Helper()
	r.t.Logf("%s -> err=%v (want kind %v)", op, err, want)
	if !errors.Is(err, want) {
		r.t.Fatalf("%s: err=%v, want kind %v", op, err, want)
	}
}

// installToActive registers, updates and installs a version that
// activates immediately because the active slot is empty.
func (r *testRig) installToActive(scope, script, digest string, manifest map[string]string) uint64 {
	r.t.Helper()
	r.digests[script] = digest
	r.mustRegister(scope, script)
	id := r.mustUpdate(scope)
	r.mustInstall(id, manifest)
	r.wantState(id, StateActive)
	return id
}

func TestNestedScopeLongestMatch(t *testing.T) {
	rig := newRig(t, 0)
	vRoot := rig.installToActive("/", "root.js", "d-root", nil)
	vApp := rig.installToActive("/app/", "app.js", "d-app", nil)
	vAdmin := rig.installToActive("/app/admin/", "admin.js", "d-admin", nil)

	rig.mustNavigate("c-root", "/other/page")
	rig.wantClient("c-root", vRoot)
	rig.mustNavigate("c-app", "/app/page")
	rig.wantClient("c-app", vApp)
	rig.mustNavigate("c-admin", "/app/admin/panel")
	rig.wantClient("c-admin", vAdmin)
	rig.mustNavigate("c-noscope", "")

	rig2 := newRig(t, 0)
	rig2.installToActive("/only/", "x.js", "d-x", nil)
	rig2.mustNavigate("c-free", "/elsewhere/")
	rig2.wantClient("c-free", 0)
	rig2.wantFetch("c-free", "/only/a", "", true)
	t.Log("verdict: longest prefix wins; no matching scope means uncontrolled")
}

func TestDuplicateRegisterSameScriptNoop(t *testing.T) {
	rig := newRig(t, time.Minute)
	rig.digests["sw.js"] = "d1"
	rig.mustRegister("/app/", "sw.js")
	id := rig.mustUpdate("/app/")

	existed, err := rig.c.Register("/app/", "sw.js")
	rig.wantErr("Register dup", err, nil)
	_ = existed
	if !existed {
		t.Fatal("duplicate register should report existed=true")
	}
	info, _ := rig.c.RegistrationInfo("/app/")
	t.Logf("after dup: installing=%d lastCheck=%v", info.Installing, info.LastCheck)
	if info.Installing != id {
		t.Fatalf("no-op register changed installing slot to %d", info.Installing)
	}
	if _, created, err := rig.c.CheckUpdate("/app/"); !errors.Is(err, ErrTooFrequent) || created {
		t.Fatalf("dup register must not count as an update check: created=%v err=%v", created, err)
	}
}

func TestSameDigestProducesNoVersion(t *testing.T) {
	rig := newRig(t, time.Second)
	rig.digests["sw.js"] = "same"
	rig.mustRegister("/app/", "sw.js")
	first := rig.mustUpdate("/app/")
	rig.advance(2 * time.Second)
	id, created, err := rig.c.CheckUpdate("/app/")
	t.Logf("second CheckUpdate -> id=%d created=%v err=%v", id, created, err)
	if err != nil || created {
		t.Fatalf("identical digest must not create a version: created=%v err=%v", created, err)
	}
	rig.wantState(first, StateInstalling)
}

func TestWaitingDisplacedByNewerVersion(t *testing.T) {
	rig := newRig(t, 0)
	rig.digests["sw.js"] = "d1"
	rig.mustRegister("/app/", "sw.js")
	v1 := rig.mustUpdate("/app/")
	rig.mustInstall(v1, nil)
	rig.wantState(v1, StateActive)
	rig.mustNavigate("c1", "/app/")

	rig.digests["sw.js"] = "d2"
	v2 := rig.mustUpdate("/app/")
	rig.mustInstall(v2, nil)
	rig.wantState(v2, StateWaiting) // blocked: active still controls c1

	rig.digests["sw.js"] = "d3"
	v3 := rig.mustUpdate("/app/")
	rig.mustInstall(v3, nil)
	rig.wantState(v2, StateRedundant) // displaced by newer waiting version
	rig.wantState(v3, StateWaiting)
	rig.wantState(v1, StateActive)
	t.Log("verdict: waiting slot keeps only the newest successfully installed version")
}

func TestTakeoverWhenClientCountHitsZero(t *testing.T) {
	rig := newRig(t, 0)
	v1 := rig.installToActive("/app/", "sw.js", "d1", nil)
	rig.mustNavigate("c1", "/app/")
	rig.mustNavigate("c2", "/app/")

	rig.digests["sw.js"] = "d2"
	v2 := rig.mustUpdate("/app/")
	rig.mustInstall(v2, nil)
	rig.wantState(v2, StateWaiting)

	rig.mustUnload("c1")
	rig.wantState(v2, StateWaiting) // still one client left
	rig.mustUnload("c2")
	rig.wantState(v2, StateActive) // count hit zero -> takeover
	rig.wantState(v1, StateRedundant)
	t.Log("verdict: takeover fires exactly when the active client count reaches zero")
}

func TestSkipWaitingAndClaimCombinations(t *testing.T) {
	combos := []struct {
		name  string
		skip  bool
		claim bool
	}{
		{"skip+claim", true, true},
		{"skip+noclaim", true, false},
		{"noskip+claim", false, true},
		{"noskip+noclaim", false, false},
	}
	for _, combo := range combos {
		t.Run(combo.name, func(t *testing.T) {
			rig := newRig(t, 0)
			v1 := rig.installToActive("/app/", "sw.js", "d1", nil)
			rig.mustNavigate("c1", "/app/")
			rig.wantClient("c1", v1)

			rig.digests["sw.js"] = "d2"
			v2 := rig.mustUpdate("/app/")
			if err := rig.c.Declare(v2, TakeoverOptions{
				SkipWaiting:  combo.skip,
				ClaimClients: combo.claim,
			}); err != nil {
				t.Fatalf("Declare: %v", err)
			}
			rig.mustInstall(v2, nil)

			if combo.skip {
				rig.wantState(v2, StateActive)
				rig.wantState(v1, StateRedundant)
			} else {
				rig.wantState(v2, StateWaiting)
				rig.wantState(v1, StateActive)
				rig.mustUnload("c1")
				rig.wantState(v2, StateActive)
			}
			if combo.skip && combo.claim {
				rig.wantClient("c1", v2) // claimed at takeover
			} else {
				// No claim: the client keeps its version for the page
				// lifetime; with noskip the client left before takeover.
				rig.wantClient("c1", map[bool]uint64{true: 0, false: v1}[!combo.skip])
			}
			t.Logf("verdict: skip=%v claim=%v reproduced", combo.skip, combo.claim)
		})
	}
}

func TestClientVersionStableWithinPageLifetime(t *testing.T) {
	rig := newRig(t, 0)
	v1 := rig.installToActive("/app/", "sw.js", "d1", map[string]string{"/a": "da"})
	rig.mustNavigate("c1", "/app/")
	rig.wantClient("c1", v1)

	rig.digests["sw.js"] = "d2"
	v2 := rig.mustUpdate("/app/")
	if err := rig.c.Declare(v2, TakeoverOptions{SkipWaiting: true}); err != nil {
		t.Fatal(err)
	}
	rig.mustInstall(v2, map[string]string{"/b": "db"})
	rig.wantState(v2, StateActive)
	rig.wantState(v1, StateRedundant)

	rig.wantClient("c1", v1) // unchanged within the page lifetime
	// v1 is redundant: its manifest is unreadable, fetch falls to network.
	rig.wantFetch("c1", "/a", "", true)

	rig.mustNavigate("c1", "/app/") // next navigation re-binds
	rig.wantClient("c1", v2)
	rig.wantFetch("c1", "/b", "db", false)
	t.Log("verdict: control survives takeover, rebinds only on navigation")
}

func TestManifestInheritanceOnlyAtTakeover(t *testing.T) {
	rig := newRig(t, 0)
	v1 := rig.installToActive("/app/", "sw.js", "d1",
		map[string]string{"/old": "d-old", "/shared": "d-v1"})
	rig.mustNavigate("c1", "/app/")

	rig.digests["sw.js"] = "d2"
	v2 := rig.mustUpdate("/app/")
	rig.mustInstall(v2, map[string]string{"/shared": "d-v2", "/new": "d-new"})
	if err := rig.c.SetManifestEntry(v1, "/late", "d-late"); err != nil {
		t.Fatalf("pre-takeover mutation of active manifest: %v", err)
	}
	if err := rig.c.Declare(v2, TakeoverOptions{SkipWaiting: true, InheritManifest: true}); err != nil {
		t.Fatal(err)
	}
	rig.wantState(v2, StateActive)
	rig.wantState(v1, StateRedundant)

	info, _ := rig.c.VersionInfo(v2)
	t.Logf("v2 manifest after takeover: %v", info.Manifest)
	want := map[string]string{"/shared": "d-v2", "/new": "d-new", "/old": "d-old", "/late": "d-late"}
	for url, digest := range want {
		if info.Manifest[url] != digest {
			t.Fatalf("manifest[%q]=%q, want %q", url, info.Manifest[url], digest)
		}
	}
	// Inheritance is a one-time snapshot: the old manifest is now frozen
	// (redundant), and any later change attempt must not leak into v2.
	rig.wantErr("SetManifestEntry on redundant v1",
		rig.c.SetManifestEntry(v1, "/old", "d-changed"), ErrStateNotAllowed)
	rig.wantFetch("c1", "/old", "", true) // c1 still bound to redundant v1
	rig.mustNavigate("c2", "/app/")
	rig.wantFetch("c2", "/old", "d-old", false)
	t.Log("verdict: inheritance copied exactly once, at the takeover moment")
}

func TestUnregisterRedundantsAfterLastClientLeaves(t *testing.T) {
	rig := newRig(t, 0)
	v1 := rig.installToActive("/app/", "sw.js", "d1", map[string]string{"/a": "da"})
	rig.mustNavigate("c1", "/app/")
	rig.mustNavigate("c2", "/app/x")

	if err := rig.c.Unregister("/app/"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	info, ok := rig.c.RegistrationInfo("/app/")
	t.Logf("after unregister: pending=%v clients=%d", info.PendingRemoval, info.ClientCount)
	if !ok || !info.PendingRemoval {
		t.Fatal("registration should be pending removal, not gone")
	}
	// Pending removal: no new clients, no update checks; existing
	// clients stay controlled.
	rig.mustNavigate("c3", "/app/")
	rig.wantClient("c3", 0)
	rig.wantErr("CheckUpdate pending removal",
		func() error { _, _, err := rig.c.CheckUpdate("/app/"); return err }(),
		ErrStateNotAllowed)
	rig.wantClient("c1", v1)
	rig.wantFetch("c1", "/a", "da", false)

	rig.mustUnload("c1")
	if _, ok := rig.c.RegistrationInfo("/app/"); !ok {
		t.Fatal("registration removed before last client left")
	}
	rig.mustUnload("c2")
	if _, ok := rig.c.RegistrationInfo("/app/"); ok {
		t.Fatal("registration should be gone after last client left")
	}
	rig.wantState(v1, StateRedundant)
	rig.wantFetch("c2", "/a", "", true)
	t.Log("verdict: registration and versions redundant only after the last client leaves")
}

func TestReregisterRevivesPendingRemoval(t *testing.T) {
	rig := newRig(t, 0)
	v1 := rig.installToActive("/app/", "sw.js", "d1", nil)
	rig.mustNavigate("c1", "/app/")

	if err := rig.c.Unregister("/app/"); err != nil {
		t.Fatal(err)
	}
	if info, _ := rig.c.RegistrationInfo("/app/"); !info.PendingRemoval {
		t.Fatal("expected pending removal")
	}
	// Re-registering the same scope cancels the pending removal and
	// revives the registration with its version relations untouched.
	existed, err := rig.c.Register("/app/", "sw.js")
	t.Logf("re-Register -> existed=%v err=%v", existed, err)
	if err != nil || !existed {
		t.Fatalf("revive: existed=%v err=%v", existed, err)
	}
	info, ok := rig.c.RegistrationInfo("/app/")
	t.Logf("after revive: pending=%v active=%d", info.PendingRemoval, info.Active)
	if !ok || info.PendingRemoval {
		t.Fatal("registration should be alive again")
	}
	if info.Active != v1 {
		t.Fatalf("version relations changed by revive: active=%d want %d", info.Active, v1)
	}
	rig.wantClient("c1", v1) // control survived the pending-removal episode
	rig.mustNavigate("c2", "/app/")
	rig.wantClient("c2", v1) // accepts new clients again

	// Re-registering with a different script URL triggers an update
	// check (interval-free) instead of creating a new registration.
	rig2 := newRig(t, time.Hour)
	rig2.digests["a.js"] = "da"
	rig2.mustRegister("/app/", "a.js")
	rig2.mustUpdate("/app/")
	rig2.digests["b.js"] = "db"
	existed, err = rig2.c.Register("/app/", "b.js")
	t.Logf("re-Register new script -> existed=%v err=%v", existed, err)
	if err != nil || !existed {
		t.Fatalf("script-change register: existed=%v err=%v", existed, err)
	}
	info2, _ := rig2.c.RegistrationInfo("/app/")
	t.Logf("after script change: script=%s installing=%d", info2.ScriptURL, info2.Installing)
	if info2.ScriptURL != "b.js" || info2.Installing == 0 {
		t.Fatal("script change must trigger an interval-free update check")
	}
	t.Log("verdict: revive keeps versions; script change reuses the registration")
}

func TestMinIntervalExactAndMinusOne(t *testing.T) {
	const interval = 10 * time.Second
	rig := newRig(t, interval)
	rig.digests["sw.js"] = "d1"
	rig.mustRegister("/app/", "sw.js")
	rig.mustUpdate("/app/") // successful check at t0

	rig.advance(interval - 1)
	_, _, err := rig.c.CheckUpdate("/app/")
	rig.wantErr("check at interval-1", err, ErrTooFrequent)

	rig.advance(1)
	rig.digests["sw.js"] = "d2"
	id, created, err := rig.c.CheckUpdate("/app/")
	t.Logf("check at exactly interval -> id=%d created=%v err=%v", id, created, err)
	if err != nil || !created {
		t.Fatalf("check at exactly the interval must pass: created=%v err=%v", created, err)
	}
	t.Log("verdict: rejected at interval-1, accepted at exactly interval")
}

func TestErrorPrecedenceAndRejectionPurity(t *testing.T) {
	rig := newRig(t, time.Hour)
	v1 := rig.installToActive("/app/", "sw.js", "d1", map[string]string{"/a": "da"})
	rig.mustNavigate("c1", "/app/")
	rig.advance(time.Second)

	snapshot := func() string {
		reg, _ := rig.c.RegistrationInfo("/app/")
		ver, _ := rig.c.VersionInfo(v1)
		cl, controlled := rig.c.ClientInfo("c1")
		return fmt.Sprintf("%+v|%+v|%d:%v", reg, ver, cl, controlled)
	}
	before := snapshot()

	// invalid argument beats clock rollback
	rig.now = rig.now.Add(-time.Hour)
	_, err := rig.c.Register("noslash", "x.js")
	rig.wantErr("invalid scope + rolled clock", err, ErrInvalidArgument)
	// clock rollback beats registration not found
	_, _, err = rig.c.CheckUpdate("/missing/")
	rig.wantErr("rolled clock + missing scope", err, ErrClockRollback)
	rig.advance(2 * time.Hour) // clock healthy again

	// registration not found beats too frequent
	_, _, err = rig.c.CheckUpdate("/missing/")
	rig.wantErr("missing scope", err, ErrRegistrationNotFound)
	// version not found beats state not allowed
	err = rig.c.InstallSuccess(9999, nil)
	rig.wantErr("missing version", err, ErrVersionNotFound)
	// state not allowed: install an active version
	err = rig.c.InstallSuccess(v1, nil)
	rig.wantErr("install active version", err, ErrStateNotAllowed)

	if after := snapshot(); after != before {
		t.Fatalf("rejected operations mutated state:\nbefore %s\nafter  %s", before, after)
	}

	// too frequent is the last check; a same-digest check succeeds first
	if _, created, err := rig.c.CheckUpdate("/app/"); err != nil || created {
		t.Fatalf("same-digest check: created=%v err=%v", created, err)
	}
	_, _, err = rig.c.CheckUpdate("/app/")
	rig.wantErr("immediate re-check", err, ErrTooFrequent)
	t.Log("verdict: six categories ordered; rejections changed nothing")
}

func TestRedundantVersionRejectsEverything(t *testing.T) {
	rig := newRig(t, 0)
	rig.digests["sw.js"] = "d1"
	rig.mustRegister("/app/", "sw.js")
	v1 := rig.mustUpdate("/app/")
	if err := rig.c.InstallFail(v1); err != nil {
		t.Fatal(err)
	}
	rig.wantState(v1, StateRedundant)
	info, _ := rig.c.RegistrationInfo("/app/")
	if info.Installing != 0 {
		t.Fatal("failed install must vacate the installing slot")
	}
	rig.wantErr("install redundant", rig.c.InstallSuccess(v1, nil), ErrStateNotAllowed)
	rig.wantErr("fail redundant", rig.c.InstallFail(v1), ErrStateNotAllowed)
	rig.wantErr("declare redundant", rig.c.Declare(v1, TakeoverOptions{}), ErrStateNotAllowed)
	rig.wantErr("manifest redundant", rig.c.SetManifestEntry(v1, "/x", "d"), ErrStateNotAllowed)
	t.Log("verdict: redundant is terminal for every operation")
}

func TestClockRollbackRejectsAndDoesNotStick(t *testing.T) {
	rig := newRig(t, 0)
	v1 := rig.installToActive("/app/", "sw.js", "d1", nil)
	rig.advance(time.Minute)
	rig.mustNavigate("c0", "/app/") // successful op at the high-water time

	rig.now = rig.now.Add(-time.Second)
	rig.wantErr("navigate with rolled clock", rig.c.Navigate("c1", "/app/"), ErrClockRollback)
	if _, controlled := rig.c.ClientInfo("c1"); controlled {
		t.Fatal("rejected navigate must not control the client")
	}
	rig.advance(2 * time.Second) // past lastTime again
	rig.mustNavigate("c1", "/app/")
	rig.wantClient("c1", v1)
	t.Log("verdict: rollback rejects once; the clock itself is never mutated")
}

func TestFetchAnsweredByControllingManifest(t *testing.T) {
	rig := newRig(t, 0)
	rig.installToActive("/app/", "sw.js", "d1", map[string]string{"/hit": "d-hit"})
	rig.mustNavigate("c1", "/app/")
	rig.wantFetch("c1", "/hit", "d-hit", false) // manifest hit
	rig.wantFetch("c1", "/miss", "", true)      // manifest miss
	rig.wantFetch("ghost", "/hit", "", true)    // unknown client
	t.Log("verdict: hit returns the digest, miss and uncontrolled return network")
}

// TestConcurrentInvariants hammers one coordinator from many goroutines
// and checks the serial-order invariants afterwards: at most one
// version per slot, and every controlled client bound to a version
// that is active or was active before going redundant.
func TestConcurrentInvariants(t *testing.T) {
	var clockMu sync.Mutex
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	c := New(Options{
		Now: func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		},
		ScriptDigest: func(string) (string, error) { return "d0", nil },
	})
	if _, err := c.Register("/app/", "sw.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Register("/app/sub/", "sw.js"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			client := fmt.Sprintf("c%d", g)
			for i := 0; i < 200; i++ {
				clockMu.Lock()
				now = now.Add(time.Millisecond)
				clockMu.Unlock()
				switch rng.Intn(6) {
				case 0:
					id, _, err := c.CheckUpdate("/app/")
					if err == nil && id != 0 {
						_ = c.InstallSuccess(id, map[string]string{"/x": "dx"})
					}
				case 1:
					_ = c.Navigate(client, "/app/page")
				case 2:
					_ = c.Unload(client)
				case 3:
					_, _ = c.Fetch(client, "/x")
				case 4:
					_, _ = c.Register("/app/", "sw.js")
				case 5:
					_ = c.Unregister("/app/sub/")
					_, _ = c.Register("/app/sub/", "sw.js")
				}
			}
		}(g)
	}
	wg.Wait()

	info, _ := c.RegistrationInfo("/app/")
	t.Logf("final: installing=%d waiting=%d active=%d clients=%d",
		info.Installing, info.Waiting, info.Active, info.ClientCount)
	for g := 0; g < 8; g++ {
		verID, controlled := c.ClientInfo(fmt.Sprintf("c%d", g))
		if !controlled {
			continue
		}
		vinfo, _ := c.VersionInfo(verID)
		if vinfo.State != StateActive && vinfo.State != StateRedundant {
			t.Fatalf("client c%d controlled by %s version %d", g, vinfo.State, verID)
		}
	}
	t.Log("verdict: concurrent use keeps slot and control invariants")
}
