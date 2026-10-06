package bcontext

import (
	"errors"
	"strings"
	"testing"
)

func testCfg() Config {
	return Config{
		// camera defaults to self-only; geo defaults to all origins.
		DefaultAllowAll: map[string]bool{"camera": false, "geo": true},
		// sab is only usable in cross-origin isolated documents.
		RequiresIsolation: map[string]bool{"sab": true},
	}
}

var (
	originA = "https://a.example"
	originB = "https://b.example"
)

func isoHeader(origin string, coop OpenerPolicy) Header {
	return Header{
		Origin:   origin,
		Opener:   coop,
		Embedder: EmbedderRequireCorp,
		Allow:    map[string][]string{},
	}
}

func credLessHeader(origin string, coop OpenerPolicy) Header {
	h := isoHeader(origin, coop)
	h.Embedder = EmbedderCredentialless
	return h
}

func nonIsoHeader(origin string, coop OpenerPolicy) Header {
	return Header{Origin: origin, Opener: coop, Embedder: EmbedderUnsafeNone}
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func assertBool(t *testing.T, name string, got, want bool) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

// 1) Top document isolation: each of the two required headers missing in turn.
func TestTopIsolationHeaderMissing(t *testing.T) {
	k := New(testCfg())

	top, err := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	if err != nil {
		t.Fatal(err)
	}
	if iso, _ := k.Isolated(top); !iso {
		t.Fatal("full headers must isolate")
	}

	noCoop, _ := k.LoadTop(nonIsoHeader(originA, OpenerNone))
	if iso, _ := k.Isolated(noCoop); iso {
		t.Fatal("coop=none must not isolate")
	}

	noCoep, _ := k.LoadTop(nonIsoHeader(originA, OpenerSameOrigin))
	if iso, _ := k.Isolated(noCoep); iso {
		t.Fatal("coep=unsafe-none must not isolate")
	}

	cl, _ := k.LoadTop(credLessHeader(originA, OpenerSameOrigin))
	if iso, _ := k.Isolated(cl); !iso {
		t.Fatal("coep=credentialless must isolate with coop=same-origin")
	}
}

// 2) Descendant declares everything but a non-isolated top poisons subtree.
func TestNonIsolatedTopPoisonsSubtree(t *testing.T) {
	k := New(testCfg())
	top, _ := k.LoadTop(nonIsoHeader(originA, OpenerNone))
	child, err := k.LoadFrame(top, isoHeader(originB, OpenerSameOrigin), FrameAllow{})
	if err != nil {
		t.Fatal(err)
	}
	if iso, _ := k.Isolated(child); iso {
		t.Fatal("child must not isolate under non-isolated top")
	}
	grand, _ := k.LoadFrame(child, isoHeader(originA, OpenerSameOrigin), FrameAllow{})
	if iso, _ := k.Isolated(grand); iso {
		t.Fatal("grandchild must not isolate")
	}
}

// 3) Embed admission differs for same-origin vs cross-origin children.
func TestEmbedAdmission(t *testing.T) {
	k := New(testCfg())
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))

	_, err := k.LoadFrame(top, nonIsoHeader(originB, OpenerNone), FrameAllow{})
	assertErrIs(t, err, ErrEmbedPolicy)

	same, err := k.LoadFrame(top, nonIsoHeader(originA, OpenerNone), FrameAllow{})
	if err != nil {
		t.Fatalf("same-origin child admitted: %v", err)
	}
	if iso, _ := k.Isolated(same); iso {
		t.Fatal("coep-less same-origin child must not isolate")
	}

	x, err := k.LoadFrame(top, isoHeader(originB, OpenerSameOrigin), FrameAllow{})
	if err != nil {
		t.Fatal(err)
	}
	if iso, _ := k.Isolated(x); !iso {
		t.Fatal("cross-origin credentialed child must isolate")
	}

	loose, _ := k.LoadTop(nonIsoHeader(originA, OpenerNone))
	if _, err := k.LoadFrame(loose, nonIsoHeader(originB, OpenerNone), FrameAllow{}); err != nil {
		t.Fatalf("coep-less parent must embed anyone: %v", err)
	}
}

// 4) Opener policies: grouping and severing across the three modes.
func TestOpenerGrouping(t *testing.T) {
	k := New(testCfg())

	topNone, _ := k.LoadTop(nonIsoHeader(originA, OpenerNone))
	p1, _ := k.OpenPopup(topNone, nonIsoHeader(originB, OpenerNone))
	if opener, ok, err := k.OpenerReference(p1); err != nil || !ok || opener != topNone {
		t.Fatalf("none/none popup must stay grouped: opener=%v ok=%v err=%v", opener, ok, err)
	}

	topSO, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	p2, _ := k.OpenPopup(topSO, nonIsoHeader(originB, OpenerNone))
	_, _, err := k.OpenerReference(p2)
	assertErrIs(t, err, ErrOpenerBroken)

	p3, _ := k.OpenPopup(topSO, isoHeader(originA, OpenerSameOrigin))
	if opener, ok, err := k.OpenerReference(p3); err != nil || !ok || opener != topSO {
		t.Fatalf("same-origin popup must stay grouped: %v", err)
	}

	topAllow, _ := k.LoadTop(isoHeader(originA, OpenerSameOriginAllowPopups))
	p4, _ := k.OpenPopup(topAllow, nonIsoHeader(originB, OpenerNone))
	if opener, ok, err := k.OpenerReference(p4); err != nil || !ok || opener != topAllow {
		t.Fatalf("allow-popups must keep cross-origin popup: %v", err)
	}
	p5, _ := k.OpenPopup(topAllow, isoHeader(originB, OpenerSameOrigin))
	_, _, err = k.OpenerReference(p5)
	assertErrIs(t, err, ErrOpenerBroken)
}

// 5) Severance never restores after later navigation.
func TestBrokenGroupNotRestored(t *testing.T) {
	k := New(testCfg())
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	popup, _ := k.OpenPopup(top, nonIsoHeader(originB, OpenerNone))
	_, _, err := k.OpenerReference(popup)
	assertErrIs(t, err, ErrOpenerBroken)

	if err := k.Navigate(popup, isoHeader(originA, OpenerSameOrigin), nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = k.OpenerReference(popup)
	assertErrIs(t, err, ErrOpenerBroken)
}

// 6) Feature evaluation: each of the three subframe conditions missing in turn.
func TestFeatureThreeConditions(t *testing.T) {
	topHeader := isoHeader(originA, OpenerSameOrigin)
	topHeader.Allow["camera"] = []string{originA}
	childHeader := isoHeader(originB, OpenerSameOrigin)

	k := New(testCfg())
	top, _ := k.LoadTop(topHeader)
	child, err := k.LoadFrame(top, childHeader, FrameAllow{
		Allow: map[string][]string{"camera": {originB}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := k.FeatureAllowed(child, "camera"); !ok {
		t.Fatal("camera should be available with all three conditions")
	}

	strictTop := isoHeader(originA, OpenerSameOrigin)
	strictTop.Allow["camera"] = []string{}
	k2 := New(testCfg())
	t2, _ := k2.LoadTop(strictTop)
	c2, _ := k2.LoadFrame(t2, childHeader, FrameAllow{Allow: map[string][]string{"camera": {"*"}}})
	if ok, _ := k2.FeatureAllowed(c2, "camera"); ok {
		t.Fatal("unavailable when parent lacks the feature")
	}

	k3 := New(testCfg())
	t3, _ := k3.LoadTop(topHeader)
	c3, _ := k3.LoadFrame(t3, childHeader, FrameAllow{})
	if ok, _ := k3.FeatureAllowed(c3, "camera"); ok {
		t.Fatal("unavailable when edge does not list child origin")
	}

	denySelf := isoHeader(originB, OpenerSameOrigin)
	denySelf.Allow["camera"] = []string{}
	k4 := New(testCfg())
	t4, _ := k4.LoadTop(topHeader)
	c4, _ := k4.LoadFrame(t4, denySelf, FrameAllow{Allow: map[string][]string{"camera": {originB}}})
	if ok, _ := k4.FeatureAllowed(c4, "camera"); ok {
		t.Fatal("unavailable when child excludes own origin")
	}
}

// 7) Default allow lists: self-only vs all for same/cross children.
func TestFeatureDefaultLists(t *testing.T) {
	k := New(testCfg())
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	sameChild, _ := k.LoadFrame(top, isoHeader(originA, OpenerSameOrigin), FrameAllow{})
	crossChild, err := k.LoadFrame(top, isoHeader(originB, OpenerSameOrigin), FrameAllow{})
	if err != nil {
		t.Fatal(err)
	}

	if ok, _ := k.FeatureAllowed(sameChild, "camera"); !ok {
		t.Fatal("self default must allow same-origin child")
	}
	if ok, _ := k.FeatureAllowed(crossChild, "camera"); ok {
		t.Fatal("self default must deny cross-origin child")
	}
	if ok, _ := k.FeatureAllowed(sameChild, "geo"); !ok {
		t.Fatal("all default must allow same-origin child")
	}
	if ok, _ := k.FeatureAllowed(crossChild, "geo"); !ok {
		t.Fatal("all default must allow cross-origin child")
	}
}

// 8) Isolation-required feature: four combinations of isolation x policy.
func TestIsolationRequiredFeature(t *testing.T) {
	cases := []struct {
		name    string
		top     Header
		allow   map[string][]string
		wantSAB bool
	}{
		{"isolated+allowed", isoHeader(originA, OpenerSameOrigin), map[string][]string{"sab": {"*"}}, true},
		{"isolated+denied", isoHeader(originA, OpenerSameOrigin), map[string][]string{"sab": {}}, false},
		{"nonisolated+allowed", nonIsoHeader(originA, OpenerNone), map[string][]string{"sab": {"*"}}, false},
		{"nonisolated+denied", nonIsoHeader(originA, OpenerNone), map[string][]string{"sab": {}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := New(testCfg())
			tc.top.Allow = tc.allow
			top, err := k.LoadTop(tc.top)
			if err != nil {
				t.Fatal(err)
			}
			ok, _ := k.FeatureAllowed(top, "sab")
			assertBool(t, "sab", ok, tc.wantSAB)
		})
	}
}

// 9) Child navigation re-derives isolation/policy; parent chain unchanged.
func TestChildNavigationReDerives(t *testing.T) {
	k := New(testCfg())
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))

	// Same-origin child admitted with unsafe-none: not isolated (own COEP fail).
	child, err := k.LoadFrame(top, nonIsoHeader(originA, OpenerNone),
		FrameAllow{Allow: map[string][]string{"camera": {originA}}})
	if err != nil {
		t.Fatal(err)
	}
	if iso, _ := k.Isolated(child); iso {
		t.Fatal("same-origin coep-less child must not isolate")
	}

	// Navigate (same origin, so no admission restriction) to credentialed COEP:
	// isolation is re-derived from the new headers and flips to true.
	if err := k.Navigate(child, isoHeader(originA, OpenerNone), nil); err != nil {
		t.Fatal(err)
	}
	if iso, _ := k.Isolated(child); !iso {
		t.Fatal("isolation must be re-derived after child navigation")
	}

	// Cross-origin frame: navigation re-derives policy; the frame allow list is
	// fixed at frame creation, so the camera grant survives new documents.
	denySelf := isoHeader(originB, OpenerSameOrigin)
	denySelf.Allow["camera"] = []string{}
	cross, _ := k.LoadFrame(top, denySelf, FrameAllow{
		Allow: map[string][]string{"camera": {originB}},
	})
	if ok, _ := k.FeatureAllowed(cross, "camera"); ok {
		t.Fatal("new document denies itself")
	}
	if err := k.Navigate(cross, isoHeader(originB, OpenerSameOrigin), nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := k.FeatureAllowed(cross, "camera"); !ok {
		t.Fatal("fixed frame allow list must grant camera after re-navigation")
	}
	if iso, _ := k.Isolated(cross); !iso {
		t.Fatal("cross credentialed frame stays isolated")
	}

	// Cross-origin navigation dropping COEP is rejected; state unchanged.
	err = k.Navigate(cross, nonIsoHeader(originB, OpenerNone), nil)
	assertErrIs(t, err, ErrEmbedPolicy)
	if iso, _ := k.Isolated(cross); !iso {
		t.Fatal("rejected navigation must not change isolation")
	}
}

// 10) Changing the frame allow list affects later loads only.
func TestSetFrameAllowFutureLoadsOnly(t *testing.T) {
	k := New(testCfg())
	topHeader := isoHeader(originA, OpenerSameOrigin)
	top, _ := k.LoadTop(topHeader)
	childHeader := isoHeader(originB, OpenerSameOrigin)
	child, _ := k.LoadFrame(top, childHeader, FrameAllow{
		Allow: map[string][]string{"camera": {originB}},
	})
	if ok, _ := k.FeatureAllowed(child, "camera"); !ok {
		t.Fatal("camera initially allowed")
	}

	// Restrict the edge; the already-loaded document is unaffected.
	if err := k.SetFrameAllow(child, FrameAllow{Allow: map[string][]string{"camera": {}}}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := k.FeatureAllowed(child, "camera"); !ok {
		t.Fatal("current document must keep evaluating against old edge")
	}

	// After the child navigates (new document), the pending edge takes effect.
	if err := k.Navigate(child, isoHeader(originB, OpenerSameOrigin), nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := k.FeatureAllowed(child, "camera"); ok {
		t.Fatal("new document must evaluate against the pending edge")
	}

	// A grandchild in a separately created frame is governed by that frame's
	// own allow list (independently of the child's edge change).
	grand, _ := k.LoadFrame(child, isoHeader(originB, OpenerSameOrigin), FrameAllow{
		Allow: map[string][]string{"camera": {originB}},
	})
	if ok, _ := k.FeatureAllowed(grand, "camera"); ok {
		t.Fatal("grandchild edge must be independent of child's edge change")
	}
	_ = grand
}

// 11) Parent navigation replaces the subtree; descendants report no document.
func TestParentNavigationTombstonesSubtree(t *testing.T) {
	k := New(testCfg())
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	child, _ := k.LoadFrame(top, isoHeader(originB, OpenerSameOrigin), FrameAllow{})
	grand, _ := k.LoadFrame(child, isoHeader(originA, OpenerSameOrigin), FrameAllow{})

	if err := k.Navigate(top, isoHeader(originA, OpenerSameOrigin), nil); err != nil {
		t.Fatal(err)
	}

	// New top document is alive.
	if iso, _ := k.Isolated(top); !iso {
		t.Fatal("top still isolated after navigation")
	}
	// Old subtree ids remain known contexts but have no document.
	_, err := k.Isolated(child)
	assertErrIs(t, err, ErrNoDocument)
	_, err = k.Isolated(grand)
	assertErrIs(t, err, ErrNoDocument)

	// Distinguishable from a never-existing context.
	_, err = k.Isolated(999999)
	assertErrIs(t, err, ErrNoContext)

	// Tombstoned contexts cannot be navigated, embedded into or modified.
	err = k.Navigate(grand, isoHeader(originA, OpenerSameOrigin), nil)
	assertErrIs(t, err, ErrNoDocument)
	_, err = k.LoadFrame(grand, isoHeader(originA, OpenerSameOrigin), FrameAllow{})
	assertErrIs(t, err, ErrNoDocument)
	err = k.SetFrameAllow(child, FrameAllow{})
	assertErrIs(t, err, ErrNoDocument)
}

// 12) Rejection order: invalid < no context < no document < embed < broken < unknown.
func TestRejectionOrder(t *testing.T) {
	k := New(testCfg())

	// Invalid beats nonexistent context.
	_, err := k.LoadFrame(999, Header{Origin: ""}, FrameAllow{})
	assertErrIs(t, err, ErrInvalidArgument)

	// Nonexistent context beats embed mismatch (a violating cross child).
	_, err = k.LoadFrame(999, nonIsoHeader(originB, OpenerNone), FrameAllow{})
	assertErrIs(t, err, ErrNoContext)

	// Tombstone beats embed mismatch.
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	child, _ := k.LoadFrame(top, isoHeader(originB, OpenerSameOrigin), FrameAllow{})
	_ = k.Navigate(top, isoHeader(originA, OpenerSameOrigin), nil)
	_, err = k.LoadFrame(child, nonIsoHeader(originB, OpenerNone), FrameAllow{})
	assertErrIs(t, err, ErrNoDocument)

	// Embed mismatch beats unknown feature (child both non-credentialed and
	// declaring an unknown feature).
	violating := nonIsoHeader(originB, OpenerNone)
	violating.Allow = map[string][]string{"mystery": {originB}}
	otherTop, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	_, err = k.LoadFrame(otherTop, violating, FrameAllow{Allow: map[string][]string{"mystery": {"*"}}})
	assertErrIs(t, err, ErrEmbedPolicy)

	// Unknown feature surfaces after admission passes.
	bad := isoHeader(originB, OpenerSameOrigin)
	bad.Allow = map[string][]string{"mystery": {originB}}
	_, err = k.LoadFrame(otherTop, bad, FrameAllow{})
	assertErrIs(t, err, ErrUnknownFeature)

	// Unknown policy value is an invalid argument regardless of context.
	_, err = k.LoadFrame(otherTop, Header{Origin: originB, Opener: "weird"}, FrameAllow{})
	assertErrIs(t, err, ErrInvalidArgument)

	// Queries: broken group vs unknown feature are different entry points;
	// broken group is reported from OpenerReference, unknown from features.
	popup, _ := k.OpenPopup(otherTop, nonIsoHeader(originB, OpenerNone))
	_, _, err = k.OpenerReference(popup)
	assertErrIs(t, err, ErrOpenerBroken)
	// Build a live popup and confirm unknown-feature ordering after existence.
	_, err = k.FeatureAllowed(otherTop, "mystery")
	assertErrIs(t, err, ErrUnknownFeature)
	_, err = k.FeatureAllowed(999, "mystery")
	assertErrIs(t, err, ErrNoContext)
	_, err = k.FeatureAllowed(child, "mystery")
	assertErrIs(t, err, ErrNoDocument)
}

// 13) Rejected operations change nothing.
func TestRejectionIsAtomic(t *testing.T) {
	k := New(testCfg())
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	before := len(k.contexts)
	_, err := k.LoadFrame(top, nonIsoHeader(originB, OpenerNone),
		FrameAllow{Allow: map[string][]string{"mystery": {"*"}}})
	assertErrIs(t, err, ErrEmbedPolicy)
	if len(k.contexts) != before {
		t.Fatal("rejected LoadFrame must not create a context")
	}
	err = k.Navigate(top, Header{Origin: originB, Opener: "bad"}, nil)
	assertErrIs(t, err, ErrInvalidArgument)
	if cur, _ := k.Isolated(top); !cur {
		t.Fatal("rejected navigation must not change document")
	}
}

// 14) Logging includes inputs, outputs and decision basis.
type captureLogger struct{ lines []string }

func (c *captureLogger) Log(s string) { c.lines = append(c.lines, s) }

func TestLoggerContent(t *testing.T) {
	lg := &captureLogger{}
	k := New(testCfg()).WithLogger(lg)
	top, _ := k.LoadTop(isoHeader(originA, OpenerSameOrigin))
	_, _ = k.LoadFrame(top, isoHeader(originB, OpenerSameOrigin), FrameAllow{
		Allow: map[string][]string{"camera": {originB}},
	})
	_, _ = k.FeatureAllowed(top, "camera")

	joined := strings.Join(lg.lines, "\n")
	for _, want := range []string{"LoadTop", "origin=", "ACCEPT", "isolated=true", "FeatureAllowed", "true"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("log missing %q:\n%s", want, joined)
		}
	}
	_ = errors.Is
}
