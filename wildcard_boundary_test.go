package projection

// Characterization tests for the under-specified wildcard boundaries:
//   - "prefix.*" only matches child keys that pass validName; real map keys
//     with spaces, dots or non-ASCII letters silently miss the wildcard;
//   - exact deny cascades to descendants but wildcard deny does not;
//   - specificity() has only two tiers, so wildcard-vs-wildcard ties are
//     decided by "deny wins" / slice order and Explain does not report that
//     a tie was adjudicated.
//
// These tests pin the CURRENT implementation behavior (not desired behavior).

import "testing"

// oddKeyObject has one legal child key and four child keys that Compile
// itself would reject inside a pattern.
func oddKeyObject() map[string]any {
	return map[string]any{
		"addr": map[string]any{
			"street":     1,
			"first name": 2,
			"café":       3,
			"a.b":        4,
			"姓名":         5,
		},
	}
}

func TestWildcardCharacterization(t *testing.T) {
	// Case 1: which real child keys does "addr.*" actually govern?
	t.Run("wildcard_only_matches_validName_child_keys", func(t *testing.T) {
		cases := []struct {
			name string
			cfg  Config
		}{
			// DefaultAllow + deny "addr.*": a governed key is hidden, an
			// ungoverned key falls through to default allow.
			{name: "deny-side", cfg: Config{DefaultAllow: true, Deny: []string{"addr.*"}}},
			// Default deny + allow "addr.*": a governed key is visible, an
			// ungoverned key falls through to default deny.
			{name: "allow-side", cfg: Config{Allow: []string{"addr.*"}}},
		}
		keys := []struct {
			key   string
			valid bool
		}{
			{"street", true},
			{"first name", false},
			{"café", false},
			{"a.b", false},
			{"姓名", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rs, err := Compile(tc.cfg)
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				out, err := rs.Project(oddKeyObject(), nil)
				if err != nil {
					t.Fatalf("project: %v", err)
				}
				addr, ok := out["addr"].(map[string]any)
				if !ok {
					t.Fatalf("addr missing or wrong type in %#v", out)
				}
				for _, k := range keys {
					_, present := addr[k.key]
					var wantPresent bool
					switch tc.name {
					case "deny-side":
						// Only the validName child is denied; the odd keys
						// silently miss "addr.*" and stay default-visible.
						wantPresent = !k.valid
					case "allow-side":
						// Only the validName child is allowed; the odd keys
						// miss the allow and stay default-denied.
						wantPresent = k.valid
					}
					if present != wantPresent {
						t.Errorf("key %q (validName=%v) present=%v, want %v", k.key, k.valid, present, wantPresent)
					}
				}
			})
		}
	})

	// Dotted Explain input cannot even name the "a.b" child: it splits into
	// a third path segment, so "addr.*" misses it by depth as well.
	t.Run("Explain_dotted_odd_key_misses_by_depth", func(t *testing.T) {
		rs, _ := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
		d := rs.Explain("addr.a.b")
		if d.Reason != ReasonDefault || !d.Visible || d.RuleRaw != "" {
			t.Fatalf("dotted odd key must fall to default, got %+v", d)
		}
	})

	// Case 2: exact deny cascades to deeper descendants; wildcard deny
	// governs direct children only.
	t.Run("exact_deny_cascades_wildcard_deny_does_not", func(t *testing.T) {
		cases := []struct {
			name        string
			cfg         Config
			wantVisible bool
			wantReason  Reason
			wantRule    string
		}{
			{
				name:        "exact deny on ancestor",
				cfg:         Config{Allow: []string{"addr.geo.lat"}, Deny: []string{"addr"}},
				wantVisible: false,
				wantReason:  ReasonAncestorOverride,
				wantRule:    "addr",
			},
			{
				name:        "wildcard deny does not cascade",
				cfg:         Config{DefaultAllow: true, Deny: []string{"addr.*"}},
				wantVisible: true,
				wantReason:  ReasonDefault,
				wantRule:    "",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rs, err := Compile(tc.cfg)
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				d := rs.Explain("addr.geo.lat")
				if d.Visible != tc.wantVisible || d.Reason != tc.wantReason || d.RuleRaw != tc.wantRule {
					t.Fatalf("addr.geo.lat decision = %+v, want visible=%v reason=%v rule=%q",
						d, tc.wantVisible, tc.wantReason, tc.wantRule)
				}
			})
		}

		// Project-level mirror: wildcard deny hides the direct child key
		// "geo", but its surviving grandchild keeps the intermediate object
		// alive; exact deny removes the whole subtree.
		obj := map[string]any{"addr": map[string]any{"geo": map[string]any{"lat": 1}}}

		rsWild, _ := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
		out, err := rsWild.Project(obj, nil)
		if err != nil {
			t.Fatalf("project wildcard: %v", err)
		}
		geo, ok := out["addr"].(map[string]any)["geo"].(map[string]any)
		if !ok || geo["lat"] != 1 {
			t.Fatalf("addr.geo.lat must survive wildcard deny via surviving child, got %#v", out)
		}

		rsExact, _ := Compile(Config{Allow: []string{"addr.geo.lat"}, Deny: []string{"addr"}})
		out, err = rsExact.Project(obj, nil)
		if err != nil {
			t.Fatalf("project exact: %v", err)
		}
		if _, ok := out["addr"]; ok {
			t.Fatalf("exact deny must remove the whole addr subtree, got %#v", out)
		}
	})

	// Case 3: equal-specificity wildcard ties. Two DISTINCT valid wildcard
	// patterns can never both match the same field through Compile (the
	// wildcard is always the final segment, so identical prefixes would
	// mean identical pattern text); the ties that can exist are therefore
	// same-text rules on opposite sides or duplicated/same-prefix rules on
	// one side. directMatch has no depth/length tie-break and Explain has no
	// field reporting that adjudication happened.
	t.Run("distinct_compiled_wildcards_cannot_tie", func(t *testing.T) {
		wildcards := []pattern{
			{raw: "a.*", segments: []string{"a"}, wildcard: true},
			{raw: "b.*", segments: []string{"b"}, wildcard: true},
			{raw: "a.b.*", segments: []string{"a", "b"}, wildcard: true},
			{raw: "x.y.*", segments: []string{"x", "y"}, wildcard: true},
		}
		candidates := [][]string{
			{"a", "k"}, {"b", "k"},
			{"a", "b", "k"}, {"x", "y", "k"},
		}
		for i := 0; i < len(wildcards); i++ {
			for j := i + 1; j < len(wildcards); j++ {
				for _, path := range candidates {
					if wildcards[i].matchesDirect(path) && wildcards[j].matchesDirect(path) {
						t.Fatalf("distinct wildcards %q and %q both matched %v",
							wildcards[i].raw, wildcards[j].raw, path)
					}
				}
			}
		}
	})

	t.Run("same_text_wildcard_allow_and_deny_tie_deny_wins_silently", func(t *testing.T) {
		// Bypasses Compile's same-text conflict check the way an internally
		// assembled ruleset could; both rules genuinely match addr.city.
		wp := pattern{raw: "addr.*", segments: []string{"addr"}, wildcard: true}
		rs := &Ruleset{
			allow:        []compiledRule{{pattern: wp, index: 1}},
			deny:         []compiledRule{{pattern: wp, index: 1}},
			defaultAllow: false,
			exactDeny:    map[string]compiledRule{},
		}
		d := rs.Explain("addr.city")
		want := Decision{
			Path:    "addr.city",
			Visible: false,
			Effect:  EffectDeny,
			Reason:  ReasonDirectRule,
			RuleRaw: "addr.*",
		}
		if d != want {
			t.Fatalf("tie decision = %+v, want %+v (no field records the tie)", d, want)
		}
	})

	t.Run("same_side_wildcard_tie_first_slice_entry_wins_silently", func(t *testing.T) {
		// Same prefix (so both match), different raw text: only achievable
		// internally, and it exposes the lack of any secondary ordering.
		first := pattern{raw: "FIRST.*", segments: []string{"addr"}, wildcard: true}
		second := pattern{raw: "SECOND.*", segments: []string{"addr"}, wildcard: true}
		rs := &Ruleset{
			allow:        []compiledRule{{pattern: first, index: 1}, {pattern: second, index: 2}},
			defaultAllow: false,
			exactDeny:    map[string]compiledRule{},
		}
		d := rs.Explain("addr.city")
		if !d.Visible || d.Reason != ReasonDirectRule || d.RuleRaw != "FIRST.*" {
			t.Fatalf("first slice entry must win with no tie signal, got %+v", d)
		}

		// Reversing the slice reverses the outcome: order-dependent, not
		// depth/length-dependent.
		rs2 := &Ruleset{
			allow:        []compiledRule{{pattern: second, index: 1}, {pattern: first, index: 2}},
			defaultAllow: false,
			exactDeny:    map[string]compiledRule{},
		}
		if d := rs2.Explain("addr.city"); d.RuleRaw != "SECOND.*" {
			t.Fatalf("winner must follow slice order, got %+v", d)
		}
	})
}
