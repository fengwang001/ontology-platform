package clearance

import (
	"errors"
	"testing"

	"ontology/grant"
	"ontology/territory"
)

func bs(s string) []byte { return []byte(s) }

func setupExample(t *testing.T) (*grant.Registry, *Judge) {
	t.Helper()
	tr, err := territory.NewTree(map[string][]string{
		territory.Root: {"EU", "AS"},
		"EU":           {"FR", "DE"},
		"AS":           {"JP", "KR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := grant.NewRegistry(tr)
	must := func(id, who, node string, ex []string, s, e int64, exl bool) {
		_, err := r.Add(100, bs(id), bs("T"), bs(who), node, ex, s, e, exl)
		if err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	must("g1", "甲", "EU", []string{"FR"}, 100, 200, true)
	must("g2", "乙", territory.Root, []string{"DE"}, 150, 300, true)
	must("g4", "丙", "DE", nil, 200, 250, false)
	if err := r.Revoke(160, bs("g1")); err != nil {
		t.Fatal(err)
	}
	return r, New(r)
}

func TestCanPlayExample(t *testing.T) {
	_, j := setupExample(t)
	cases := []struct {
		name      string
		licensee  string
		leaf      string
		at        int64
		wantKind  string
		wantGrant string
	}{
		{"allowed own grant", "丙", "DE", 205, Allow, "g4"},
		{"blocked by exclusive", "丙", "FR", 205, Exclusive, "g2"},
		{"no grant after window", "丙", "DE", 260, None, ""},
		{"t==end is inactive", "甲", "DE", 160, None, ""},
		{"t<start not active", "丙", "DE", 199, None, ""},
		{"exclusive holder itself allowed", "乙", "FR", 205, Allow, "g2"},
		{"stranger has no own grant even on non-exclusive leaf", "庚", "DE", 205, None, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := j.CanPlay(bs("T"), bs(tc.licensee), bs(tc.leaf), tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if res.Kind != tc.wantKind {
				t.Fatalf("kind=%s want %s", res.Kind, tc.wantKind)
			}
			if tc.wantGrant != "" {
				if res.Grant == nil || string(res.Grant.ID) != tc.wantGrant {
					got := ""
					if res.Grant != nil {
						got = string(res.Grant.ID)
					}
					t.Fatalf("grant=%s want %s", got, tc.wantGrant)
				}
			}
		})
	}
}

func TestCanPlayValidationOrder(t *testing.T) {
	_, j := setupExample(t)
	if _, err := j.CanPlay(nil, bs("丙"), bs("DE"), 200); !errors.Is(err, grant.ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	if _, err := j.CanPlay(bs("T"), bs("丙"), bs("DE"), grant.MaxTime+1); !errors.Is(err, grant.ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	if _, err := j.CanPlay(bs("T"), bs("丙"), bs("MARS"), 200); !errors.Is(err, grant.ErrUnknownNode) {
		t.Fatalf("got %v", err)
	}
	if _, err := j.CanPlay(bs("T"), bs("丙"), bs("EU"), 200); !errors.Is(err, grant.ErrNotALeaf) {
		t.Fatalf("got %v", err)
	}
	if _, err := j.Holders(bs("T"), bs("EU"), 200); !errors.Is(err, grant.ErrNotALeaf) {
		t.Fatalf("got %v", err)
	}
}

func TestHoldersOrderAndRevoke(t *testing.T) {
	tr, err := territory.NewTree(map[string][]string{territory.Root: {"A"}})
	if err != nil {
		t.Fatal(err)
	}
	r := grant.NewRegistry(tr)
	j := New(r)
	add := func(id, who string, s, e int64) {
		if _, err := r.Add(1, bs(id), bs("M"), bs(who), "A", nil, s, e, false); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	add("z", "Z", 0, 100)
	add("a", "A", 0, 100)
	add("m", "M", 0, 100)
	got, err := j.Holders(bs("M"), bs("A"), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || string(got[0].ID) != "a" || string(got[1].ID) != "m" || string(got[2].ID) != "z" {
		t.Fatalf("holders = %v", ids(got))
	}
	if err := r.Revoke(60, bs("m")); err != nil {
		t.Fatal(err)
	}
	got, _ = j.Holders(bs("M"), bs("A"), 50)
	if len(got) != 3 {
		t.Fatalf("before revoke time holders=%v", ids(got))
	}
	got, _ = j.Holders(bs("M"), bs("A"), 70)
	if len(got) != 2 || string(got[0].ID) != "a" || string(got[1].ID) != "z" {
		t.Fatalf("after revoke holders=%v", ids(got))
	}
	if err := r.Revoke(70, bs("a")); err != nil { // start=0 < 70 < end=100：截断到 70
		t.Fatal(err)
	}
	got, _ = j.Holders(bs("M"), bs("A"), 75)
	if len(got) != 1 || string(got[0].ID) != "z" {
		t.Fatalf("a truncated to 70, holders at 75 = %v", ids(got))
	}
}

func ids(gs []*grant.Grant) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = string(g.ID)
	}
	return out
}
