package adjust

import (
	"errors"
	"testing"
)

func TestAddLoc(t *testing.T) {
	cases := []struct {
		name   string
		id     []byte
		book   int64
		price  int64
		second func(db *DB) error
		want   error
	}{
		{"ok", []byte("X"), 100, 10, nil, nil},
		{"empty id", nil, 0, 0, nil, ErrInvalid},
		{"id too long", make([]byte, 33), 0, 0, nil, ErrInvalid},
		{"book negative", []byte("a"), -1, 0, nil, ErrInvalid},
		{"book too big", []byte("a"), 1_000_000_001, 0, nil, ErrInvalid},
		{"price negative", []byte("a"), 0, -1, nil, ErrInvalid},
		{"price too big", []byte("a"), 0, 1_000_001, nil, ErrInvalid},
		{"dup id", []byte("X"), 1, 1, func(db *DB) error { return db.AddLoc([]byte("X"), 1, 1) }, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := New(2, 5, 500)
			err := db.AddLoc(tc.id, tc.book, tc.price)
			if err == nil && tc.second != nil {
				err = tc.second(db)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMove(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(db *DB)
		id       []byte
		delta    int64
		wantErr  error
		wantBook int64
		wantMv   int64
	}{
		{"in", nil, []byte("X"), 5, nil, 105, 5},
		{"out", nil, []byte("X"), -30, nil, 70, -30},
		{"zero rejected", nil, []byte("X"), 0, ErrInvalid, 100, 0},
		{"delta too big", nil, []byte("X"), 1_000_000_001, ErrInvalid, 100, 0},
		{"underflow", nil, []byte("X"), -101, ErrStock, 100, 0},
		{"overflow", func(db *DB) { _ = db.Move([]byte("X"), 999_999_900) }, []byte("X"), 20, ErrInvalid, 1_000_000_000, 999_999_900},
		{"unknown loc", nil, []byte("Z"), 1, ErrNotFound, 0, 0},
		{"bad id", nil, []byte{}, 1, ErrInvalid, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := New(2, 5, 500)
			if err := db.AddLoc([]byte("X"), 100, 10); err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				tc.setup(db)
			}
			err := db.Move(tc.id, tc.delta)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if string(tc.id) == "X" {
				book, _ := db.Book([]byte("X"))
				mv, _ := db.Moved([]byte("X"))
				if book != tc.wantBook || mv != tc.wantMv {
					t.Fatalf("book=%d mv=%d, want %d/%d", book, mv, tc.wantBook, tc.wantMv)
				}
			}
		})
	}
}

func TestTol(t *testing.T) {
	// tabs=2, tpct=5：book=100 -> max(2,5)=5；book=200 -> 10；book=10 -> 5 vs 2 -> 5? floor(10*5/100)=0 -> 2。
	db := New(2, 5, 500)
	if err := db.AddLoc([]byte("X"), 100, 10); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Tol([]byte("X"))
	if got != 5 {
		t.Fatalf("tol(100)=%d want 5", got)
	}
	_ = db.Move([]byte("X"), 100)
	got, _ = db.Tol([]byte("X"))
	if got != 10 {
		t.Fatalf("tol(200)=%d want 10", got)
	}
	_ = db.Move([]byte("X"), -190)
	got, _ = db.Tol([]byte("X"))
	if got != 2 {
		t.Fatalf("tol(10)=%d want 2", got)
	}
	// book=0 时比例项为 0，容差退化为绝对容差。
	if err := db.AddLoc([]byte("Z"), 0, 1); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Tol([]byte("Z"))
	if got != 2 {
		t.Fatalf("tol(0)=%d want 2", got)
	}
}

func TestTolEqualBoundary(t *testing.T) {
	// 题目例：book=100, tol=5；counted=95 恰等容差，判据由 count 包负责，
	// 这里验证 Tol 边界与 |diff| 恰等的语义。
	db := New(2, 5, 500)
	_ = db.AddLoc([]byte("X"), 100, 10)
	tol, _ := db.Tol([]byte("X"))
	diff := int64(-5)
	if diff < 0 {
		diff = -diff
	}
	if !(diff <= tol) {
		t.Fatalf("|diff| == tol must be within tolerance")
	}
}
