package policy

import (
	"reflect"
	"testing"
)

func TestPolicy(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		grants  map[string][]string
		role    string
		wantAll []string
		wantVis []string
		wantHid []string
		wantKn  map[string]bool
	}{
		{
			name:    "subset role gets sorted projection",
			columns: []string{"c", "a", "b", "a"},
			grants:  map[string][]string{"r1": {"b", "a", "x"}},
			role:    "r1",
			wantAll: []string{"a", "b", "c"},
			wantVis: []string{"a", "b"},
			wantHid: []string{"c"},
			wantKn:  map[string]bool{"a": true, "x": false},
		},
		{
			name:    "unknown role sees nothing",
			columns: []string{"a", "b"},
			grants:  map[string][]string{"r1": {"a", "b"}},
			role:    "ghost",
			wantAll: []string{"a", "b"},
			wantVis: []string{},
			wantHid: []string{"a", "b"},
			wantKn:  map[string]bool{"a": true},
		},
		{
			name:    "full visibility",
			columns: []string{"a"},
			grants:  map[string][]string{"r1": {"a"}},
			role:    "r1",
			wantAll: []string{"a"},
			wantVis: []string{"a"},
			wantHid: []string{},
			wantKn:  map[string]bool{"a": true, "b": false},
		},
		{
			name:    "empty universe",
			columns: nil,
			grants:  map[string][]string{"r1": {"a"}},
			role:    "r1",
			wantAll: []string{},
			wantVis: []string{},
			wantHid: []string{},
			wantKn:  map[string]bool{"a": false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.columns, tt.grants)
			if got := p.Columns(); !reflect.DeepEqual(got, tt.wantAll) {
				t.Fatalf("Columns = %v, want %v", got, tt.wantAll)
			}
			if got := p.Visible(tt.role); !reflect.DeepEqual(got, tt.wantVis) {
				t.Fatalf("Visible = %v, want %v", got, tt.wantVis)
			}
			if got := p.Hidden(tt.role); !reflect.DeepEqual(got, tt.wantHid) {
				t.Fatalf("Hidden = %v, want %v", got, tt.wantHid)
			}
			for col, want := range tt.wantKn {
				if got := p.Knows(col); got != want {
					t.Fatalf("Knows(%q) = %v, want %v", col, got, want)
				}
				if want {
					if got := p.IsVisible(tt.role, col); got != (contains(tt.wantVis, col)) {
						t.Fatalf("IsVisible(%q) = %v", col, got)
					}
				}
			}
		})
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
