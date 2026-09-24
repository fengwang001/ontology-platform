package policy

import (
	"errors"
	"reflect"
	"testing"
)

func TestPolicy(t *testing.T) {
	p := New(map[string][]string{
		"analyst": {"id", "name", "id"},
		"blind":   {},
		"all":     {"id", "name", "secret"},
	})

	tests := []struct {
		name    string
		role    string
		want    []string
		wantErr error
		canSee  bool
		col     string
	}{
		{"normal dedup sorted", "analyst", []string{"id", "name"}, nil, true, "name"},
		{"empty set", "blind", nil, ErrEmptyRole, false, "id"},
		{"full set", "all", []string{"id", "name", "secret"}, nil, true, "secret"},
		{"unknown role", "ghost", nil, ErrUnknownRole, false, "id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Visible(tt.role)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Visible err = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Visible = %v, want %v", got, tt.want)
			}
			if ok := p.CanSee(tt.role, tt.col); ok != tt.canSee {
				t.Fatalf("CanSee = %v, want %v", ok, tt.canSee)
			}
		})
	}

	if roles := p.Roles(); !reflect.DeepEqual(roles, []string{"all", "analyst", "blind"}) {
		t.Fatalf("Roles = %v, want sorted", roles)
	}
	set, err := p.VisibleSet("analyst")
	if err != nil || len(set) != 2 {
		t.Fatalf("VisibleSet = %v, %v", set, err)
	}
}
