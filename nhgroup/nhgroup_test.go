package nhgroup

import (
	"errors"
	"testing"
)

func TestTargetBuckets(t *testing.T) {
	tests := []struct {
		name     string
		count    int
		members  []Member
		want     map[uint32]int
		balanced bool
	}{
		{
			name:  "equal split",
			count: 8,
			members: []Member{
				{Nexthop: 1, Weight: 1, Alive: true},
				{Nexthop: 2, Weight: 1, Alive: true},
			},
			want:     map[uint32]int{1: 4, 2: 4},
			balanced: true,
		},
		{
			name:  "remainder tie uses smaller nexthop",
			count: 8,
			members: []Member{
				{Nexthop: 1, Weight: 1, Alive: true},
				{Nexthop: 2, Weight: 1, Alive: true},
				{Nexthop: 3, Weight: 1, Alive: true},
			},
			want:     map[uint32]int{1: 3, 2: 3, 3: 2},
			balanced: true,
		},
		{
			name:  "larger remainder wins",
			count: 10,
			members: []Member{
				{Nexthop: 1, Weight: 6, Alive: true},
				{Nexthop: 2, Weight: 5, Alive: true},
			},
			want:     map[uint32]int{1: 5, 2: 5},
			balanced: true,
		},
		{
			name:  "dead members excluded",
			count: 4,
			members: []Member{
				{Nexthop: 1, Weight: 1, Alive: true},
				{Nexthop: 2, Weight: 9, Alive: false},
			},
			want:     map[uint32]int{1: 4, 2: 0},
			balanced: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TargetBuckets(tt.count, tt.members)
			for nexthop, want := range tt.want {
				if got.Buckets[nexthop] != want {
					t.Fatalf("member %d target = %d, want %d", nexthop, got.Buckets[nexthop], want)
				}
			}
			current := map[uint32]int{}
			for nexthop, count := range got.Buckets {
				current[nexthop] = count
			}
			alive := 0
			for _, member := range tt.members {
				if member.Alive {
					alive++
				}
			}
			if got := Balance(tt.count, current, got, alive); got != tt.balanced {
				t.Fatalf("Balance = %v, want %v", got, tt.balanced)
			}
		})
	}
}

func TestValidateMember(t *testing.T) {
	valid := []Member{{Nexthop: 1, Weight: 1, Alive: true}, {Nexthop: 1_000_000, Weight: 1000, Alive: true}}
	for _, member := range valid {
		if err := ValidateMember(member.Nexthop, member.Weight); err != nil {
			t.Fatalf("ValidateMember(%+v): %v", member, err)
		}
	}
	invalid := []Member{{}, {Nexthop: 1_000_001, Weight: 1}, {Nexthop: 1, Weight: 1001}}
	for _, member := range invalid {
		if err := ValidateMember(member.Nexthop, member.Weight); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("ValidateMember(%+v) error = %v, want invalid", member, err)
		}
	}
}
