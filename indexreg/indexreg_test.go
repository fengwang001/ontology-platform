package indexreg_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/indexreg"
)

func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"a", true},
		{"a1", true},
		{"a-b_c", true},
		{"index_01", true},
		{strings.Repeat("a", 64), true},
		{"", false},
		{"A", false},
		{"1", true},
		{"-a", false},
		{"_a", false},
		{"a.b", false},
		{"a b", false},
		{"a/b", false},
		{strings.Repeat("a", 65), false},
	}
	for _, tc := range cases {
		if got := indexreg.ValidName(tc.name); got != tc.want {
			t.Errorf("ValidName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRegistryLifecycle(t *testing.T) {
	r := indexreg.New()
	if r.Epoch() != 0 {
		t.Fatalf("initial epoch = %d, want 0", r.Epoch())
	}

	// 非法名优先于冲突。
	if err := r.CreateIndex("bad name"); !errors.Is(err, indexreg.ErrInvalidName) {
		t.Fatalf("CreateIndex invalid: %v", err)
	}
	if r.Epoch() != 0 {
		t.Fatalf("rejected op bumped epoch to %d", r.Epoch())
	}

	if err := r.CreateIndex("i1"); err != nil {
		t.Fatalf("CreateIndex i1: %v", err)
	}
	if r.Epoch() != 1 {
		t.Fatalf("epoch = %d, want 1", r.Epoch())
	}
	// 与现有索引同名 -> 冲突。
	if err := r.CreateIndex("i1"); !errors.Is(err, indexreg.ErrNameConflict) {
		t.Fatalf("dup CreateIndex: %v", err)
	}
	// 非法名次序高于冲突。
	if err := r.CreateIndex(""); !errors.Is(err, indexreg.ErrInvalidName) {
		t.Fatalf("invalid before conflict: %v", err)
	}
	if r.Epoch() != 1 {
		t.Fatalf("epoch changed after rejects: %d", r.Epoch())
	}

	// Close：不存在、状态变化、幂等空操作。
	if err := r.CloseIndex("missing"); !errors.Is(err, indexreg.ErrIndexNotFound) {
		t.Fatalf("CloseIndex missing: %v", err)
	}
	if err := r.CloseIndex("i1"); err != nil {
		t.Fatalf("CloseIndex i1: %v", err)
	}
	if r.Epoch() != 2 {
		t.Fatalf("epoch = %d, want 2", r.Epoch())
	}
	if st, _ := r.StateOf("i1"); st != indexreg.Closed {
		t.Fatalf("i1 state = %v, want Closed", st)
	}
	if err := r.CloseIndex("i1"); err != nil {
		t.Fatalf("CloseIndex again should be no-op: %v", err)
	}
	if r.Epoch() != 2 {
		t.Fatalf("no-op close bumped epoch to %d", r.Epoch())
	}

	// Open：不存在、状态变化、幂等空操作。
	if err := r.OpenIndex("missing"); !errors.Is(err, indexreg.ErrIndexNotFound) {
		t.Fatalf("OpenIndex missing: %v", err)
	}
	if err := r.OpenIndex("i1"); err != nil {
		t.Fatalf("OpenIndex i1: %v", err)
	}
	if r.Epoch() != 3 {
		t.Fatalf("epoch = %d, want 3", r.Epoch())
	}
	if err := r.OpenIndex("i1"); err != nil {
		t.Fatalf("OpenIndex again should be no-op: %v", err)
	}
	if r.Epoch() != 3 {
		t.Fatalf("no-op open bumped epoch to %d", r.Epoch())
	}
}
