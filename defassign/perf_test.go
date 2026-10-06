package defassign

import "testing"

// These tests prove the two complexity guarantees with the instrumentation
// counters in Stats:
//
//  1. The cost of judging a single read point depends only on the number of
//     paths merged at that point, not on the total program length.
//  2. The cost of one merge does not depend on the number of variables not
//     involved in that merge.

// buildChain returns a program that assigns n dummy variables and then
// reads x once. The read point has exactly one arriving path.
func buildChain(n int) *Registry {
	reg := NewRegistry()
	reg.Declare("x")
	var ids []int
	for i := 0; i < n; i++ {
		v := dummyVar(i)
		reg.Declare(v)
		ids = append(ids, reg.Assign(v))
	}
	ids = append(ids, reg.Read("x"))
	reg.SetRoot(reg.Block(ids...))
	return reg
}

func dummyVar(i int) string {
	return "d" + string(rune('a'+i%26)) + string(rune('0'+i/26%10)) + string(rune('0'+i/260%10))
}

func TestReadCostIndependentOfProgramLength(t *testing.T) {
	_, statsSmall, err := CheckWithStats(buildChain(10))
	if err != nil {
		t.Fatal(err)
	}
	_, statsLarge, err := CheckWithStats(buildChain(2000))
	if err != nil {
		t.Fatal(err)
	}
	if statsSmall.ReadChecks != statsLarge.ReadChecks {
		t.Fatalf("read cost grows with program length: %d vs %d",
			statsSmall.ReadChecks, statsLarge.ReadChecks)
	}
	if statsSmall.ReadChecks != 1 {
		t.Fatalf("single-path read must inspect exactly 1 slice, got %d", statsSmall.ReadChecks)
	}
}

// buildBranching prefixes n dummy assignments before an if/else whose arms
// both assign x, followed by a read of x. The merge at the read involves
// exactly two paths and one variable, no matter how many unrelated
// variables were assigned earlier.
func buildBranching(n int) *Registry {
	reg := NewRegistry()
	reg.Declare("x")
	var ids []int
	for i := 0; i < n; i++ {
		v := dummyVar(i)
		reg.Declare(v)
		ids = append(ids, reg.Assign(v))
	}
	a1 := reg.Assign("x")
	a2 := reg.Assign("x")
	ids = append(ids, reg.If(CondUnknown, []int{a1}, []int{a2}))
	ids = append(ids, reg.Read("x"))
	reg.SetRoot(reg.Block(ids...))
	return reg
}

func TestMergeCostIndependentOfUnrelatedVars(t *testing.T) {
	_, statsBase, err := CheckWithStats(buildBranching(0))
	if err != nil {
		t.Fatal(err)
	}
	_, statsLarge, err := CheckWithStats(buildBranching(500))
	if err != nil {
		t.Fatal(err)
	}
	if statsBase.MergeOps != statsLarge.MergeOps {
		t.Fatalf("merge cost grows with unrelated variables: %d vs %d",
			statsBase.MergeOps, statsLarge.MergeOps)
	}
	// Exactly two slices are deposited at the join (one per arm).
	if statsBase.MergeOps != 2 {
		t.Fatalf("expected 2 merge deposits at the join, got %d", statsBase.MergeOps)
	}
	// The read at the join inspects exactly the two merged paths.
	if statsBase.ReadChecks != 2 || statsLarge.ReadChecks != 2 {
		t.Fatalf("read at join must inspect 2 slices: %d / %d",
			statsBase.ReadChecks, statsLarge.ReadChecks)
	}
}
