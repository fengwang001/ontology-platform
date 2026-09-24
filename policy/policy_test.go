package policy

import "testing"

func TestPolicy(t *testing.T) {
	p := New(map[string][]string{
		"none":  {},
		"some":  {"b", "a"},
		"all":   {"a", "b", "c"},
		"empty": {},
	})
	tests := []struct {
		name   string
		role   string
		col    string
		want   bool
		has    bool
		setLen int
	}{
		{"empty role unknown col", "none", "a", false, true, 0},
		{"visible unordered insert", "some", "a", true, true, 2},
		{"some role hidden col", "some", "c", false, true, 2},
		{"all role sees col", "all", "c", true, true, 3},
		{"unknown role", "ghost", "a", false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Visible(tt.role, tt.col); got != tt.want {
				t.Fatalf("Visible = %v, want %v", got, tt.want)
			}
			if got := p.HasRole(tt.role); got != tt.has {
				t.Fatalf("HasRole = %v, want %v", got, tt.has)
			}
			if got := p.VisibleSet(tt.role); len(got) != tt.setLen {
				t.Fatalf("VisibleSet len = %d, want %d", len(got), tt.setLen)
			}
		})
	}
	got := p.VisibleSet("some")
	if len(got) == 2 && (got[0] != "a" || got[1] != "b") {
		t.Fatalf("VisibleSet not sorted: %v", got)
	}
	if got := p.VisibleSet("ghost"); got == nil || len(got) != 0 {
		t.Fatalf("unknown role set = %v, want empty non-nil", got)
	}
}
