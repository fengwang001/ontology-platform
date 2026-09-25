package projection

import "testing"

// Characterization tests: these pin the CURRENT behavior of wildcard
// matching at its edges (child-key name restriction, non-cascading wildcard
// deny, tie adjudication observability). They assert what the code does
// today, not what the documentation implies it should do.

// childKeys covers one valid identifier and four child-key names that fall
// outside [a-zA-Z0-9_-]: space, non-ASCII, embedded dot, CJK.
var childKeys = []struct {
	name string
	key  string
	want bool // matched by "addr.*" today
}{
	{"ascii identifier", "street", true},
	{"key with space", "first name", false},
	{"non-ascii letter", "café", false},
	{"key with dot", "a.b", false},
	{"cjk key", "姓名", false},
}

// TestWildcardDenyOnlyMatchesValidChildNames pins that a deny wildcard
// "addr.*" hides only children whose key is a valid identifier; every other
// real map key silently falls through to the default effect.
func TestWildcardDenyOnlyMatchesValidChildNames(t *testing.T) {
	rs, err := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range childKeys {
		t.Run(tc.name, func(t *testing.T) {
			obj := map[string]any{"addr": map[string]any{tc.key: 1}}
			out, err := rs.Project(obj, nil)
			if err != nil {
				t.Fatal(err)
			}
			addr, ok := out["addr"].(map[string]any)
			_, visible := addr[tc.key]
			if !ok {
				visible = false // whole addr object pruned
			}
			if want := !tc.want; visible != want {
				t.Fatalf("key %q: visible=%v, want %v (wildcard match=%v)",
					tc.key, visible, want, tc.want)
			}
		})
	}
}

// TestWildcardAllowOnlyMatchesValidChildNames pins the mirror image under an
// allow wildcard with a deny default: oddly-named children are silently
// hidden even though "addr.*" appears to allow all of addr's children.
func TestWildcardAllowOnlyMatchesValidChildNames(t *testing.T) {
	rs, err := Compile(Config{Allow: []string{"addr.*"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range childKeys {
		t.Run(tc.name, func(t *testing.T) {
			obj := map[string]any{"addr": map[string]any{tc.key: 1}}
			out, err := rs.Project(obj, nil)
			if err != nil {
				t.Fatal(err)
			}
			addr, ok := out["addr"].(map[string]any)
			_, visible := addr[tc.key]
			if !ok {
				visible = false
			}
			if visible != tc.want {
				t.Fatalf("key %q: visible=%v, want %v", tc.key, visible, tc.want)
			}
		})
	}
}

// TestExplainWildcardNameRestriction pins the same restriction through
// Explain. Keys containing a dot are excluded here because Explain splits
// the query on ".", so "addr.a.b" becomes a three-segment path and misses
// the wildcard for a different (length) reason.
func TestExplainWildcardNameRestriction(t *testing.T) {
	rs, err := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range childKeys {
		if tc.key == "a.b" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			d := rs.Explain("addr." + tc.key)
			if tc.want {
				if d.Visible || d.Reason != ReasonDirectRule || d.RuleRaw != "addr.*" {
					t.Fatalf("key %q should be denied by addr.*: %+v", tc.key, d)
				}
				return
			}
			// Unmatched keys fall to the default effect, silently.
			if !d.Visible || d.Reason != ReasonDefault || d.RuleRaw != "" {
				t.Fatalf("key %q should fall through to default: %+v", tc.key, d)
			}
		})
	}
}

// TestExactDenyCascadesButWildcardDenyDoesNot pins the asymmetry on the
// deep descendant addr.geo.lat: an exact deny on addr cascades to it
// (ancestor override), while a wildcard deny "addr.*" leaves it to the
// default effect.
func TestExactDenyCascadesButWildcardDenyDoesNot(t *testing.T) {
	cases := []struct {
		name         string
		cfg          Config
		wantVisible  bool
		wantReason   Reason
		wantRuleRaw  string
		wantOverride string
	}{
		{
			name:         "exact deny on addr cascades",
			cfg:          Config{DefaultAllow: true, Deny: []string{"addr"}},
			wantVisible:  false,
			wantReason:   ReasonAncestorOverride,
			wantRuleRaw:  "addr",
			wantOverride: "addr",
		},
		{
			name:         "exact deny on addr.geo cascades",
			cfg:          Config{DefaultAllow: true, Deny: []string{"addr.geo"}},
			wantVisible:  false,
			wantReason:   ReasonAncestorOverride,
			wantRuleRaw:  "addr.geo",
			wantOverride: "addr.geo",
		},
		{
			name:        "wildcard deny addr.* does not cascade",
			cfg:         Config{DefaultAllow: true, Deny: []string{"addr.*"}},
			wantVisible: true,
			wantReason:  ReasonDefault,
		},
		{
			name:        "wildcard deny addr.geo.* matches lat directly",
			cfg:         Config{DefaultAllow: true, Deny: []string{"addr.*", "addr.geo.*"}},
			wantVisible: false,
			wantReason:  ReasonDirectRule,
			wantRuleRaw: "addr.geo.*",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs, err := Compile(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			d := rs.Explain("addr.geo.lat")
			if d.Visible != tc.wantVisible || d.Reason != tc.wantReason ||
				d.RuleRaw != tc.wantRuleRaw || d.OverriddenBy != tc.wantOverride {
				t.Fatalf("addr.geo.lat decision: %+v, want visible=%v reason=%v rule=%q overriddenBy=%q",
					d, tc.wantVisible, tc.wantReason, tc.wantRuleRaw, tc.wantOverride)
			}
		})
	}
}

// TestWildcardTieAdjudication pins what happens when two rules contest the
// same field at equal specificity, and what Explain reveals about it.
//
// Today specificity has only two buckets (exact=2, wildcard=1), and two
// DISTINCT wildcard patterns can never match the same field (a wildcard's
// prefix must equal the field's parent path exactly), so a true
// wildcard-vs-wildcard tie is unreachable: the only same-text pair is
// rejected by Compile as a conflict. The closest reachable contests are
// exact-vs-wildcard, and Explain gives no hint that any adjudication or
// losing rule was involved.
func TestWildcardTieAdjudication(t *testing.T) {
	t.Run("identical wildcard on both sides is a compile conflict", func(t *testing.T) {
		_, err := Compile(Config{Allow: []string{"addr.*"}, Deny: []string{"addr.*"}})
		if err == nil {
			t.Fatal("expected conflict error")
		}
	})

	t.Run("distinct wildcards cannot contest the same field", func(t *testing.T) {
		rs, err := Compile(Config{
			DefaultAllow: true,
			Allow:        []string{"addr.*"},
			Deny:         []string{"home.*"},
		})
		if err != nil {
			t.Fatal(err)
		}
		// addr.city is matched only by the allow wildcard; the deny
		// wildcard's different prefix can never reach it.
		if d := rs.Explain("addr.city"); !d.Visible || d.RuleRaw != "addr.*" {
			t.Fatalf("addr.city decided solely by addr.*: %+v", d)
		}
	})

	t.Run("exact beats wildcard with no adjudication reported", func(t *testing.T) {
		cases := []struct {
			name        string
			cfg         Config
			field       string
			wantVisible bool
			wantRuleRaw string
		}{
			{
				name:        "exact deny beats wildcard allow",
				cfg:         Config{Allow: []string{"addr.*"}, Deny: []string{"addr.geo"}},
				field:       "addr.geo",
				wantVisible: false,
				wantRuleRaw: "addr.geo",
			},
			{
				name:        "exact allow beats wildcard deny",
				cfg:         Config{DefaultAllow: true, Allow: []string{"addr.geo"}, Deny: []string{"addr.*"}},
				field:       "addr.geo",
				wantVisible: true,
				wantRuleRaw: "addr.geo",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rs, err := Compile(tc.cfg)
				if err != nil {
					t.Fatal(err)
				}
				d := rs.Explain(tc.field)
				if d.Visible != tc.wantVisible || d.RuleRaw != tc.wantRuleRaw {
					t.Fatalf("%s: %+v, want visible=%v rule=%q",
						tc.field, d, tc.wantVisible, tc.wantRuleRaw)
				}
				// Explain reports only the winner: Reason is DirectRule and
				// there is no indication a competing rule ever matched.
				if d.Reason != ReasonDirectRule || d.OverriddenBy != "" {
					t.Fatalf("%s: no adjudication metadata expected: %+v", tc.field, d)
				}
			})
		}
	})
}
