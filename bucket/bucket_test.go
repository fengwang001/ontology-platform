package bucket

import (
	"errors"
	"testing"

	"ontology/nhgroup"
)

func members(pairs ...uint32) []MemberSpec {
	result := make([]MemberSpec, 0, len(pairs)/2)
	for idx := 0; idx < len(pairs); idx += 2 {
		result = append(result, MemberSpec{Nexthop: pairs[idx], Weight: pairs[idx+1], Alive: true})
	}
	return result
}

func owners(g *Group) []uint32 {
	result := make([]uint32, len(g.buckets))
	for idx, entry := range g.buckets {
		result[idx] = entry.owner
	}
	return result
}

func equalOwners(got, want []uint32) bool {
	if len(got) != len(want) {
		return false
	}
	for idx := range got {
		if got[idx] != want[idx] {
			return false
		}
	}
	return true
}

func TestCreateAndImmediateAssignment(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 8, 10, 50, members(1, 1, 2, 1), 0); err != nil {
		t.Fatal(err)
	}
	got := owners(controller.groups[1])
	want := []uint32{1, 2, 1, 2, 1, 2, 1, 2}
	if !equalOwners(got, want) {
		t.Fatalf("buckets = %v, want %v", got, want)
	}
}

func TestIdleMigrationAtExactThreshold(t *testing.T) {
	for _, now := range []uint64{104, 105} {
		name := "one-millisecond-before"
		want := []uint32{1, 2}
		if now == 105 {
			name = "exactly-idle"
			want = []uint32{1, 3}
		}
		t.Run(name, func(t *testing.T) {
			controller := NewController()
			if err := controller.CreateGroup(1, 2, 10, 0, members(1, 1, 2, 1), 0); err != nil {
				t.Fatal(err)
			}
			if _, err := controller.Lookup(1, 0, 95); err != nil {
				t.Fatal(err)
			}
			if _, err := controller.Lookup(1, 1, 95); err != nil {
				t.Fatal(err)
			}
			if err := controller.AddMember(1, 3, 2, 100); err != nil {
				t.Fatal(err)
			}
			if _, err := controller.Lookup(1, 0, now); err != nil {
				t.Fatal(err)
			}
			got := owners(controller.groups[1])
			if !equalOwners(got, want) {
				t.Fatalf("buckets = %v, want %v", got, want)
			}
		})
	}
}

func TestForcedMigrationAtExactLimit(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 8, 10, 50, members(1, 1, 2, 1), 0); err != nil {
		t.Fatal(err)
	}
	for idx := uint64(0); idx < 8; idx++ {
		if _, err := controller.Lookup(1, idx, 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := controller.AddMember(1, 3, 2, 100); err != nil {
		t.Fatal(err)
	}
	if got := owners(controller.groups[1]); !equalOwners(got, []uint32{1, 2, 1, 2, 1, 2, 1, 2}) {
		t.Fatalf("member-add changed buckets immediately: %v", got)
	}
	for now := uint64(101); now < 150; now++ {
		for idx := uint64(0); idx < 8; idx++ {
			if _, err := controller.Lookup(1, idx, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := controller.Lookup(1, 0, 149); err != nil {
		t.Fatal(err)
	}
	before := owners(controller.groups[1])
	if !equalOwners(before, []uint32{1, 2, 1, 2, 1, 2, 1, 2}) {
		t.Fatalf("buckets before forced = %v", before)
	}
	if _, err := controller.Lookup(1, 0, 150); err != nil {
		t.Fatal(err)
	}
	want := []uint32{3, 3, 3, 3, 1, 2, 1, 2}
	if got := owners(controller.groups[1]); !equalOwners(got, want) {
		t.Fatalf("buckets after forced = %v, want %v", got, want)
	}
}

func TestUnbalancedSinceNotResetByLaterChange(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 8, 1000, 50, members(1, 1, 2, 1), 0); err != nil {
		t.Fatal(err)
	}
	if err := controller.AddMember(1, 3, 2, 100); err != nil {
		t.Fatal(err)
	}
	if err := controller.SetWeight(1, 3, 3, 120); err != nil {
		t.Fatal(err)
	}
	group := controller.groups[1]
	if group.unbalancedSince != 100 {
		t.Fatalf("unbalancedSince = %d, want 100", group.unbalancedSince)
	}
}

func TestRemoveMemberImmediatelyAssignsBuckets(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 8, 10, 0, members(1, 1, 2, 1, 3, 2), 0); err != nil {
		t.Fatal(err)
	}
	group := controller.groups[1]
	copy(group.buckets, []bucketEntry{{1, 0}, {2, 0}, {3, 0}, {3, 0}, {3, 0}, {3, 0}, {1, 0}, {2, 0}})
	if err := controller.RemoveMember(1, 3, 10); err != nil {
		t.Fatal(err)
	}
	want := []uint32{1, 2, 1, 2, 1, 2, 1, 2}
	if got := owners(group); !equalOwners(got, want) {
		t.Fatalf("buckets = %v, want %v", got, want)
	}
}

func TestNexthopDownKeepsMembershipAndUpRecovers(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 4, 0, 0, members(1, 1, 2, 3), 0); err != nil {
		t.Fatal(err)
	}
	if err := controller.NexthopDown(2, 10); err != nil {
		t.Fatal(err)
	}
	group := controller.groups[1]
	for _, entry := range group.buckets {
		if entry.owner != 1 {
			t.Fatalf("owner after down = %d, want 1", entry.owner)
		}
	}
	if _, ok := group.members[2]; !ok || group.members[2].alive {
		t.Fatal("down member must retain membership and be marked dead")
	}
	if err := controller.NexthopDown(2, 11); err != nil {
		t.Fatal(err)
	}
	if err := controller.NexthopUp(2, 12); err != nil {
		t.Fatal(err)
	}
	if !group.members[2].alive {
		t.Fatal("member did not recover")
	}
}

func TestAllDeadLookupAndRecovery(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 2, 0, 0, members(1, 1), 0); err != nil {
		t.Fatal(err)
	}
	if err := controller.NexthopDown(1, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Lookup(1, 0, 6); !errors.Is(err, nhgroup.ErrNoNexthop) {
		t.Fatalf("Lookup error = %v, want no nexthop", err)
	}
	if got := owners(controller.groups[1]); !equalOwners(got, []uint32{0, 0}) {
		t.Fatalf("all-dead buckets = %v, want empty", got)
	}
	if err := controller.NexthopUp(1, 7); err != nil {
		t.Fatal(err)
	}
	if got := owners(controller.groups[1]); !equalOwners(got, []uint32{1, 1}) {
		t.Fatalf("recovered buckets = %v, want all member 1", got)
	}
}

func TestRejectPriorityAndRejectionHasNoSideEffect(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 1, 0, 0, members(1, 1), 0); err != nil {
		t.Fatal(err)
	}
	group := controller.groups[1]
	before := owners(group)
	if err := controller.RemoveMember(1, 2, 2); !errors.Is(err, nhgroup.ErrNotFound) {
		t.Fatalf("missing member error = %v", err)
	}
	if _, err := controller.Lookup(1, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := controller.RemoveMember(1, 1, 0); !errors.Is(err, nhgroup.ErrClockRollback) {
		t.Fatalf("rollback error = %v", err)
	}
	if err := controller.RemoveMember(1, 0, 0); !errors.Is(err, nhgroup.ErrInvalidArgument) {
		t.Fatalf("invalid argument error = %v", err)
	}
	if err := controller.RemoveMember(1, 0, 1); !errors.Is(err, nhgroup.ErrInvalidArgument) {
		t.Fatalf("second invalid argument error = %v", err)
	}
	if err := controller.RemoveMember(1, 1, 1); !errors.Is(err, nhgroup.ErrLastMember) {
		t.Fatalf("last member error = %v", err)
	}
	if got := owners(group); !equalOwners(got, before) {
		t.Fatalf("rejected operations changed buckets: %v", got)
	}
}

func TestRejectedOperationDoesNotHousekeep(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 8, 0, 0, members(1, 1, 2, 1), 0); err != nil {
		t.Fatal(err)
	}
	if err := controller.AddMember(1, 3, 2, 100); err != nil {
		t.Fatal(err)
	}
	before := owners(controller.groups[1])
	if err := controller.AddMember(1, 0, 1, 101); !errors.Is(err, nhgroup.ErrInvalidArgument) {
		t.Fatalf("invalid add error = %v", err)
	}
	if got := owners(controller.groups[1]); !equalOwners(got, before) {
		t.Fatalf("rejected operation housekept buckets: got %v, want %v", got, before)
	}
}

func TestTouchedBalancedBound(t *testing.T) {
	for _, count := range []int{64, 4096} {
		t.Run("N="+itoa(count), func(t *testing.T) {
			controller := NewController()
			membersSpec := []MemberSpec{{Nexthop: 1, Weight: 1, Alive: true}, {Nexthop: 2, Weight: 1, Alive: true}}
			if err := controller.CreateGroup(1, uint64(count), 0, 0, membersSpec, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := controller.Lookup(1, 0, 1); err != nil {
				t.Fatal(err)
			}
			if got := controller.groups[1].touched; got != 1 {
				t.Fatalf("touched = %d, want 1", got)
			}
		})
	}
}

func TestTouchedUnbalancedBound(t *testing.T) {
	controller := NewController()
	if err := controller.CreateGroup(1, 8, 0, 0, members(1, 1, 2, 1), 100); err != nil {
		t.Fatal(err)
	}
	if err := controller.CreateGroup(2, 4, 0, 0, members(3, 1, 4, 1), 100); err != nil {
		t.Fatal(err)
	}
	if err := controller.AddMember(1, 5, 2, 100); err != nil {
		t.Fatal(err)
	}
	if err := controller.AddMember(2, 6, 2, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Lookup(1, 0, 101); err != nil {
		t.Fatal(err)
	}
	touched := controller.groups[1].touched + controller.groups[2].touched
	if touched > 8+4+1 {
		t.Fatalf("touched = %d, want <= %d", touched, 13)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
