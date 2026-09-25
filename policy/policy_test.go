package policy

import (
	"errors"
	"testing"
)

func TestPolicy(t *testing.T) {
	tests := []struct {
		name    string
		grants  map[string][]string
		role    string
		col     string
		wantVis bool
		wantErr error
		wantSet []string
	}{
		{name: "visible", grants: map[string][]string{"r": {"a", "b"}}, role: "r", col: "a", wantVis: true, wantSet: []string{"a", "b"}},
		{name: "invisible", grants: map[string][]string{"r": {"a"}}, role: "r", col: "x", wantVis: false, wantSet: []string{"a"}},
		{name: "empty grant is explicit deny", grants: map[string][]string{"r": {}}, role: "r", col: "a", wantVis: false, wantSet: []string{}},
		{name: "unknown role", grants: map[string][]string{"r": {"a"}}, role: "ghost", col: "a", wantErr: ErrUnknownRole},
		{name: "duplicate columns collapse", grants: map[string][]string{"r": {"a", "a", "b"}}, role: "r", col: "b", wantVis: true, wantSet: []string{"a", "b"}},
		{name: "full set sorted", grants: map[string][]string{"r": {"c", "a", "b"}}, role: "r", col: "c", wantVis: true, wantSet: []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.grants)
			if err != nil {
				if tt.wantErr != ErrEmptyPolicy {
					t.Fatalf("New: unexpected error %v", err)
				}
				return
			}
			got, err := p.Visible(tt.role, tt.col)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Visible err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.wantVis {
				t.Fatalf("Visible = %v, want %v", got, tt.wantVis)
			}
			if tt.wantErr == nil {
				set, err := p.VisibleSet(tt.role)
				if err != nil {
					t.Fatalf("VisibleSet: %v", err)
				}
				if len(set) != len(tt.wantSet) {
					t.Fatalf("VisibleSet = %v, want %v", set, tt.wantSet)
				}
				for i := range set {
					if set[i] != tt.wantSet[i] {
						t.Fatalf("VisibleSet = %v, want %v", set, tt.wantSet)
					}
				}
			}
		})
	}
}

func TestNewErrors(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrEmptyPolicy) {
		t.Fatalf("New(nil) err = %v, want ErrEmptyPolicy", err)
	}
	p, err := New(map[string][]string{"r": {}})
	if err != nil || !p.HasRole("r") || p.HasRole("ghost") {
		t.Fatalf("empty grant handling wrong: %v %v", p, err)
	}
}
