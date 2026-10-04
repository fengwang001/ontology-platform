package shardmap_test

import (
	"errors"
	"testing"

	"ontology/shardmap"
)

func TestCreateIndexValidation(t *testing.T) {
	cases := []struct {
		name    string
		N, R, P int
		wantErr error
	}{
		{"ok", 2, 8, 1, nil},
		{"ok with partition", 4, 8, 3, nil},
		{"N zero", 0, 8, 1, shardmap.ErrInvalidParam},
		{"N too big", 1025, 2048, 1, shardmap.ErrInvalidParam},
		{"R below N", 4, 2, 1, shardmap.ErrInvalidParam},
		{"R not divisible by N", 3, 8, 1, shardmap.ErrInvalidParam},
		{"R too big", 2, 1<<20 + 2, 1, shardmap.ErrInvalidParam},
		{"P equals N", 4, 8, 4, shardmap.ErrInvalidParam},
		{"P above N", 4, 8, 5, shardmap.ErrInvalidParam},
		{"P equals one boundary", 2, 8, 2, shardmap.ErrInvalidParam},
	}
	for i, tc := range cases {
		name := tc.name
		_, err := shardmap.CreateIndex(name, tc.N, tc.R, tc.P)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("case %d (%s): err=%v want %v", i, name, err, tc.wantErr)
		}
	}
	if _, err := shardmap.CreateIndex("ok", 2, 8, 1); !errors.Is(err, shardmap.ErrIndexExists) {
		t.Fatalf("duplicate create err=%v want ErrIndexExists", err)
	}
	if _, err := shardmap.Get("missing"); !errors.Is(err, shardmap.ErrIndexNotFound) {
		t.Fatalf("get missing err=%v want ErrIndexNotFound", err)
	}
}

func TestSplitValidation(t *testing.T) {
	x, err := shardmap.CreateIndex("split-idx", 2, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		n2      int
		wantErr error
	}{
		{4, nil},
		{3, shardmap.ErrCannotSplit},
		{16, shardmap.ErrCannotSplit},
		{2, shardmap.ErrCannotSplit},
		{0, shardmap.ErrInvalidParam},
		{1025, shardmap.ErrInvalidParam},
	}
	for _, tc := range cases {
		err := x.ValidateSplit(tc.n2)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("ValidateSplit(%d) err=%v want %v", tc.n2, err, tc.wantErr)
		}
	}
	// 校验本身不改变 N。
	if got := x.Snapshot().N; got != 2 {
		t.Fatalf("N changed after validation: %d", got)
	}
	if err := x.ValidateSplit(4); err != nil {
		t.Fatal(err)
	}
	x.CommitN(4)
	if got := x.Snapshot().N; got != 4 {
		t.Fatalf("N=%d want 4 after commit", got)
	}
}

func TestShrinkValidation(t *testing.T) {
	x, err := shardmap.CreateIndex("shrink-idx", 4, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.ValidateShrink(2); !errors.Is(err, shardmap.ErrCannotShrink) {
		t.Fatalf("N2<=P: err=%v want ErrCannotShrink", err)
	}
	if err := x.ValidateShrink(3); !errors.Is(err, shardmap.ErrCannotShrink) {
		t.Fatalf("N not divisible: err=%v want ErrCannotShrink", err)
	}
	if err := x.ValidateShrink(4); !errors.Is(err, shardmap.ErrCannotShrink) {
		t.Fatalf("N2>=N: err=%v want ErrCannotShrink", err)
	}
	if err := x.ValidateShrink(0); !errors.Is(err, shardmap.ErrInvalidParam) {
		t.Fatalf("N2 out of range: err=%v want ErrInvalidParam", err)
	}

	y, err := shardmap.CreateIndex("shrink-p1", 4, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := y.ValidateShrink(2); err != nil {
		t.Fatalf("valid shrink err=%v", err)
	}
	y.CommitN(2)
	if got := y.Snapshot().N; got != 2 {
		t.Fatalf("N=%d want 2", got)
	}
}

func TestSetWriteBlock(t *testing.T) {
	x, err := shardmap.CreateIndex("block-idx", 2, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if x.WriteBlocked() {
		t.Fatal("new index should not be blocked")
	}
	if err := shardmap.SetWriteBlock("block-idx", true); err != nil {
		t.Fatal(err)
	}
	if !x.WriteBlocked() {
		t.Fatal("want blocked")
	}
	if err := shardmap.SetWriteBlock("block-idx", false); err != nil {
		t.Fatal(err)
	}
	if x.WriteBlocked() {
		t.Fatal("want unblocked")
	}
	if err := shardmap.SetWriteBlock("no-such", true); !errors.Is(err, shardmap.ErrIndexNotFound) {
		t.Fatalf("err=%v want ErrIndexNotFound", err)
	}
}
