package projection

import (
	"strings"
	"testing"
)

// This file is a characterization suite: it pins the CURRENT (possibly
// surprising) behavior of wildcard matching at three untested boundaries:
//
//  1. "addr.*" only governs child keys that pass validName; real map keys
//     with spaces, dots, or non-ASCII characters silently fall through to
//     the default effect.
//  2. only EXACT deny rules are indexed in exactDeny and cascade to
//     descendants; a wildcard deny such as "addr.*" never cascades.
//  3. specificity has only two tiers with no depth/length tie-break, and
//     Decision carries no field reporting an equal-specificity arbitration;
//     two distinct wildcard texts can in fact never co-match one field.
//
// Every assertion below describes what the implementation does today, not
// what the documentation seems to promise.

// wildcardKeyCases lists legal and illegal direct-child key names under
// "addr.*", paired with the CURRENT Explain outcome under a default-deny
// config with an "addr.*" allow rule.
func TestCharacterizationWildcardKeyNameRestriction(t *testing.T) {
	cases := []struct {
		key        string
		legalName  bool
		visible    bool
		reason     Reason
		ruleRaw    string
		projectKey bool // whether the key survives Projection
	}{
		{"street", true, true, ReasonDirectRule, "addr.*", true},
		{"first name", false, false, ReasonDefault, "", false},
		{"café", false, false, ReasonDefault, "", false},
		{"a.b", false, false, ReasonDefault, "", false},
		{"姓名", false, false, ReasonDefault, "", false},
	}

	rs, err := Compile(Config{Allow: []string{"addr.*"}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	obj := map[string]any{"addr": map[string]any{}}
	for _, c := range cases {
		obj["addr"].(map[string]any)[c.key] = 1
	}
	out, err := rs.Project(obj, nil)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	projectedAddr, _ := out["addr"].(map[string]any)

	for _, c := range cases {
		d := rs.Explain("addr." + c.key)
		if d.Visible != c.visible || d.Reason != c.reason || d.RuleRaw != c.ruleRaw {
			t.Fatalf("Explain(addr.%q) = {visible=%v reason=%v rule=%q}, want {visible=%v reason=%v rule=%q}",
				c.key, d.Visible, d.Reason, d.RuleRaw, c.visible, c.reason, c.ruleRaw)
		}
		if d.Visible && !c.legalName {
			t.Fatalf("illegal key %q unexpectedly allowed; case table is wrong", c.key)
		}
		_, present := projectedAddr[c.key]
		if present != c.projectKey {
			t.Fatalf("Project key %q present=%v, want %v (full addr projection: %#v)",
				c.key, present, c.projectKey, projectedAddr)
		}
	}

	// Mirror case under default-allow with an "addr.*" DENY: the legal key is
	// directly denied while illegal keys remain visible via the default.
	rsDeny, err := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, c := range cases {
		d := rsDeny.Explain("addr." + c.key)
		wantVisible := !c.legalName
		if d.Visible != wantVisible {
			t.Fatalf("deny-side Explain(addr.%q) visible=%v, want %v: %+v",
				c.key, d.Visible, wantVisible, d)
		}
		wantReason := ReasonDefault
		wantRule := ""
		if c.legalName {
			wantReason = ReasonDirectRule
			wantRule = "addr.*"
		}
		if d.Reason != wantReason || d.RuleRaw != wantRule {
			t.Fatalf("deny-side Explain(addr.%q) = reason=%v rule=%q, want reason=%v rule=%q",
				c.key, d.Reason, d.RuleRaw, wantReason, wantRule)
		}
	}
}

// TestCharacterizationExactVsWildcardDenyCascade pins that "addr" (exact
// deny) hides "addr.geo.lat" via ReasonAncestorOverride, while "addr.*"
// (wildcard deny) leaves it on the default effect — wildcard denies are not
// inserted into the exactDeny ancestor index.
func TestCharacterizationExactVsWildcardDenyCascade(t *testing.T) {
	cases := []struct {
		name       string
		deny       []string
		visible    bool
		reason     Reason
		ruleRaw    string
		overridden string
		geoInOut   bool // whether addr.geo (with lat) survives Projection
	}{
		{"exact deny cascades", []string{"addr"}, false, ReasonAncestorOverride, "addr", "addr", false},
		{"wildcard deny does not cascade", []string{"addr.*"}, true, ReasonDefault, "", "", true},
	}

	obj := map[string]any{
		"addr": map[string]any{
			"city": "NYC",
			"geo":  map[string]any{"lat": 40.7, "lng": -74.0},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs, err := Compile(Config{DefaultAllow: true, Deny: c.deny})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			d := rs.Explain("addr.geo.lat")
			if d.Visible != c.visible || d.Reason != c.reason ||
				d.RuleRaw != c.ruleRaw || d.OverriddenBy != c.overridden {
				t.Fatalf("Explain(addr.geo.lat) = %+v, want visible=%v reason=%v rule=%q overriddenBy=%q",
					d, c.visible, c.reason, c.ruleRaw, c.overridden)
			}

			out, err := rs.Project(obj, nil)
			if err != nil {
				t.Fatalf("project: %v", err)
			}
			addr, _ := out["addr"].(map[string]any)
			_, geoPresent := addr["geo"]
			if geoPresent != c.geoInOut {
				t.Fatalf("addr.geo present=%v, want %v (projection: %#v)",
					geoPresent, c.geoInOut, out)
			}
		})
	}
}

// TestCharacterizationWildcardTieArbitration pins the observable behavior
// when wildcard rules tie at the single wildcard specificity tier.
//
// Two DISTINCT wildcard texts can never match the same field (a wildcard
// match fixes the full prefix and key depth), so a true two-rule tie at the
// field level can only arise from the SAME text repeated — and Compile
// rejects that text across allow/deny, while a same-side duplicate is kept
// with the first occurrence winning silently. Decision exposes no field
// indicating that a tie was arbitrated.
func TestCharacterizationWildcardTieArbitration(t *testing.T) {
	distinctPairs := []struct {
		p1, p2 string
		query  []string
	}{
		{"a.*", "b.*", []string{"a", "child"}},
		{"a.*", "a.b.*", []string{"a", "b", "child"}},
		{"a.b.*", "a.*", []string{"a", "b"}},
		{"x.*", "x.y-z.*", []string{"x", "y-z", "k"}},
	}
	for _, pair := range distinctPairs {
		pat1, err := validatePattern(pair.p1)
		if err != nil {
			t.Fatalf("validate %q: %v", pair.p1, err)
		}
		pat2, err := validatePattern(pair.p2)
		if err != nil {
			t.Fatalf("validate %q: %v", pair.p2, err)
		}
		if pat1.matchesDirect(pair.query) && pat2.matchesDirect(pair.query) {
			t.Fatalf("distinct wildcards %q and %q both matched %v; a true distinct-text tie exists",
				pair.p1, pair.p2, pair.query)
		}
	}

	// Same-side duplicate wildcard: Compile accepts it, and Explain reports
	// the first occurrence only — there is no observable tie arbitration.
	rs, err := Compile(Config{Allow: []string{"addr.*", "addr.*"}})
	if err != nil {
		t.Fatalf("duplicate same-side wildcard must compile, got %v", err)
	}
	if n := len(rs.allow); n != 2 {
		t.Fatalf("both duplicate rules are stored, got %d", n)
	}
	d := rs.Explain("addr.city")
	if !d.Visible || d.Reason != ReasonDirectRule || d.RuleRaw != "addr.*" {
		t.Fatalf("duplicate allow wildcard Explain = %+v, want direct allow citing addr.*", d)
	}

	// Cross-side identical wildcard: the only way opposite rules can tie at
	// the wildcard tier; Compile rejects it as a conflict instead of
	// applying "deny wins".
	_, err = Compile(Config{Allow: []string{"addr.*"}, Deny: []string{"addr.*"}})
	if err == nil || !strings.Contains(err.Error(), "both allow and deny") {
		t.Fatalf("identical wildcard across allow/deny must be a conflict error, got %v", err)
	}
}
