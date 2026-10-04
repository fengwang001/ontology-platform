package node_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/node"
)

func TestNewClusterValidation(t *testing.T) {
	cases := []struct {
		name string
		l, h int
	}{
		{"zero low", 0, 90},
		{"high below low", 80, 70},
		{"high over 100", 80, 101},
		{"low over 100", 101, 102},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := node.NewCluster(tc.l, tc.h); !errors.Is(err, node.ErrInvalidArg) {
				t.Fatalf("want ErrInvalidArg, got %v", err)
			}
		})
	}
}

func TestValidationAndPrecedence(t *testing.T) {
	cl, _ := node.NewCluster(80, 90)
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"add empty id", func() error { return cl.AddNode("", "z", 10) }, node.ErrInvalidArg},
		{"add 65 byte id", func() error { return cl.AddNode(strings.Repeat("a", 65), "z", 10) }, node.ErrInvalidArg},
		{"add empty zone", func() error { return cl.AddNode("x", "", 10) }, node.ErrInvalidArg},
		{"add total zero", func() error { return cl.AddNode("x", "z", 0) }, node.ErrInvalidArg},
		{"add total huge", func() error { return cl.AddNode("x", "z", 1e12+1) }, node.ErrInvalidArg},
		{"add duplicate", func() error { return cl.AddNode("n1", "z", 10) }, nil},
		{"add duplicate again", func() error { return cl.AddNode("n1", "z", 10) }, node.ErrAlreadyExists},
		{"other missing node", func() error { return cl.SetOther("ghost", 1) }, node.ErrNotFound},
		{"other negative", func() error { return cl.SetOther("n1", -1) }, node.ErrInvalidArg},
		{"other too big", func() error { return cl.SetOther("n1", 1e12+1) }, node.ErrInvalidArg},
		{"exclude missing", func() error { return cl.SetExcludeLocked("ghost", true) }, node.ErrNotFound},
		{"index bad name", func() error { return cl.CreateIndex("", 1, 0, 1) }, node.ErrInvalidArg},
		{"index bad shards 0", func() error { return cl.CreateIndex("i", 0, 0, 1) }, node.ErrInvalidArg},
		{"index bad shards 65", func() error { return cl.CreateIndex("i", 65, 0, 1) }, node.ErrInvalidArg},
		{"index bad replicas 6", func() error { return cl.CreateIndex("i", 1, 6, 1) }, node.ErrInvalidArg},
		{"index bad size 0", func() error { return cl.CreateIndex("i", 1, 0, 0) }, node.ErrInvalidArg},
		{"index dup", func() error { return cl.CreateIndex("ix", 1, 0, 1) }, nil},
		{"index dup again", func() error { return cl.CreateIndex("ix", 1, 0, 1) }, node.ErrAlreadyExists},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if tc.want == nil && err != nil {
				t.Fatalf("want success, got %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestUsedAndCopies(t *testing.T) {
	cl, _ := node.NewCluster(80, 90)
	if err := cl.AddNode("n1", "z1", 1000); err != nil {
		t.Fatal(err)
	}
	if err := cl.SetOther("n1", 100); err != nil {
		t.Fatal(err)
	}
	k := node.CopyKey{Index: "i", Shard: 0, Primary: true, Size: 300}
	cl.Lock()
	cl.PutLocked("n1", k)
	if got := cl.UsedLocked("n1"); got != 400 {
		t.Fatalf("used = %d, want 400", got)
	}
	if got := cl.CopyCountLocked("n1"); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
	if id, ok := cl.PlacementLocked(k); !ok || id != "n1" {
		t.Fatalf("placement = %q,%v", id, ok)
	}
	cl.RemoveLocked("n1", k)
	if got := cl.UsedLocked("n1"); got != 100 {
		t.Fatalf("used after remove = %d, want 100", got)
	}
	cl.Unlock()
}

func TestZoneCountAndZoneCopies(t *testing.T) {
	cl, _ := node.NewCluster(80, 90)
	for _, nz := range [][2]string{{"n1", "z1"}, {"n2", "z1"}, {"n3", "z2"}} {
		if err := cl.AddNode(nz[0], nz[1], 100); err != nil {
			t.Fatal(err)
		}
	}
	cl.Lock()
	defer cl.Unlock()
	if z := cl.ZoneCountLocked(); z != 2 {
		t.Fatalf("zones = %d, want 2", z)
	}
	cl.PutLocked("n1", node.CopyKey{Index: "i", Shard: 0, Primary: true, Size: 1})
	cl.PutLocked("n2", node.CopyKey{Index: "i", Shard: 0, Replica: 0, Size: 1})
	cl.PutLocked("n3", node.CopyKey{Index: "i", Shard: 1, Primary: true, Size: 1})
	if got := cl.ZoneCopiesLocked("z1", "i", 0); got != 2 {
		t.Fatalf("zone copies = %d, want 2", got)
	}
	if got := cl.ZoneCopiesLocked("z2", "i", 0); got != 0 {
		t.Fatalf("zone copies z2 = %d, want 0", got)
	}
	if err := cl.SetExcludeLocked("n3", true); err != nil {
		t.Fatal(err)
	}
	if z := cl.ZoneCountLocked(); z != 2 {
		t.Fatalf("excluded node must still count: zones = %d", z)
	}
}

func TestRejectedOpLeavesStateUntouched(t *testing.T) {
	cl, _ := node.NewCluster(80, 90)
	_ = cl.AddNode("n1", "z1", 100)
	before := len(cl.Nodes())
	if err := cl.AddNode("n1", "z1", 100); !errors.Is(err, node.ErrAlreadyExists) {
		t.Fatalf("want exists, got %v", err)
	}
	if err := cl.SetOther("n1", -5); !errors.Is(err, node.ErrInvalidArg) {
		t.Fatalf("want invalid, got %v", err)
	}
	if got := cl.Nodes()[0].Other; got != 0 {
		t.Fatalf("state changed by rejected op: other=%d", got)
	}
	if len(cl.Nodes()) != before {
		t.Fatal("node count changed")
	}
}

func TestRejectionPrecedence(t *testing.T) {
	cl, _ := node.NewCluster(80, 90)
	_ = cl.AddNode("n1", "z1", 100)
	// Invalid argument beats "node exists": duplicate id but also invalid total.
	if err := cl.AddNode("n1", "z1", 0); !errors.Is(err, node.ErrInvalidArg) {
		t.Fatalf("invalid must beat exists, got %v", err)
	}
	// Invalid argument beats not-found.
	if err := cl.SetOther("ghost", -1); !errors.Is(err, node.ErrInvalidArg) {
		t.Fatalf("invalid must beat not found, got %v", err)
	}
	// Invalid argument beats exists for indexes.
	if err := cl.CreateIndex("dup", 0, 0, 1); !errors.Is(err, node.ErrInvalidArg) {
		t.Fatalf("invalid index must beat exists, got %v", err)
	}
	_ = cl.CreateIndex("dup", 1, 0, 1)
	if err := cl.CreateIndex("dup", 0, 0, 1); !errors.Is(err, node.ErrInvalidArg) {
		t.Fatalf("invalid must beat exists on duplicate index, got %v", err)
	}
	if err := cl.CreateIndex("dup", 1, 0, 1); !errors.Is(err, node.ErrAlreadyExists) {
		t.Fatalf("want exists, got %v", err)
	}
	// Constructor invalid watermarks.
	if _, err := node.NewCluster(0, 50); !errors.Is(err, node.ErrInvalidArg) {
		t.Fatalf("new cluster invalid low, got %v", err)
	}
	if _, err := node.NewCluster(90, 80); !errors.Is(err, node.ErrInvalidArg) {
		t.Fatalf("new cluster L>H, got %v", err)
	}
}
