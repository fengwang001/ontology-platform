package indexreg_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/alias"
	"ontology/indexreg"
)

func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"a", true},
		{"0", true},
		{"a-b_c09", true},
		{strings.Repeat("x", 64), true},
		{"", false},
		{strings.Repeat("x", 65), false},
		{"-a", false},
		{"_a", false},
		{"A", false},
		{"a b", false},
		{"a.b", false},
		{"中文", false},
	}
	for _, c := range cases {
		if got := indexreg.ValidName(c.name); got != c.ok {
			t.Errorf("ValidName(%q) = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestCreateIndexRejectOrder(t *testing.T) {
	r := indexreg.New()
	if err := r.CreateIndex("Bad"); !errors.Is(err, indexreg.ErrInvalidArgument) {
		t.Fatalf("invalid name: got %v", err)
	}
	if err := r.CreateIndex("i1"); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateIndex("i1"); !errors.Is(err, indexreg.ErrNameConflict) {
		t.Fatalf("dup index: got %v", err)
	}
	var ce *indexreg.ConflictError
	if errors.As(r.CreateIndex("i1"), &ce) && ce.Name != "i1" {
		t.Fatalf("conflict name = %q", ce.Name)
	}
	// 与现有别名同名也报名字冲突（共用名字空间）。
	if err := alias.Update(r, []alias.Action{alias.Add("a1", "i1", indexreg.WriteUnspecified, nil)}); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateIndex("a1"); !errors.Is(err, indexreg.ErrNameConflict) {
		t.Fatalf("conflict with alias: got %v", err)
	}
	if got := r.Epoch(); got != 2 {
		t.Fatalf("epoch = %d, want 2（被拒操作不加纪元）", got)
	}
}

func TestCloseOpenIndex(t *testing.T) {
	r := indexreg.New()
	if err := r.CloseIndex("nope"); !errors.Is(err, indexreg.ErrIndexNotFound) {
		t.Fatalf("close missing: got %v", err)
	}
	if err := r.OpenIndex("nope"); !errors.Is(err, indexreg.ErrIndexNotFound) {
		t.Fatalf("open missing: got %v", err)
	}
	if err := r.CreateIndex("i1"); err != nil {
		t.Fatal(err)
	}
	base := r.Epoch()
	// 状态本已如此：空操作，不加纪元。
	if err := r.OpenIndex("i1"); err != nil {
		t.Fatal(err)
	}
	if got := r.Epoch(); got != base {
		t.Fatalf("no-op open bumped epoch to %d", got)
	}
	if err := r.CloseIndex("i1"); err != nil {
		t.Fatal(err)
	}
	if got := r.Epoch(); got != base+1 {
		t.Fatalf("close did not bump epoch: %d", got)
	}
	if err := r.CloseIndex("i1"); err != nil {
		t.Fatal(err)
	}
	if got := r.Epoch(); got != base+1 {
		t.Fatalf("no-op close bumped epoch to %d", got)
	}
	if err := r.OpenIndex("i1"); err != nil {
		t.Fatal(err)
	}
	if got := r.Epoch(); got != base+2 {
		t.Fatalf("epoch = %d, want %d", got, base+2)
	}
}
