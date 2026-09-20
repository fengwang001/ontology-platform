package ontology

import (
	"errors"
	"math"
	"testing"
)

// A key typed string on the left and int64 on the right must fail with a
// decidable *KeyTypeError naming the key and both types.
func TestKeyTypeConflictIsDecidableError(t *testing.T) {
	left := []Row{{"id": "abc"}}
	right := []Row{{"id": int64(42)}}
	_, _, err := Join(left, right, []string{"id"}, Inner)
	if err == nil {
		t.Fatal("expected error for conflicting key types, got nil")
	}
	var ktErr *KeyTypeError
	if !errors.As(err, &ktErr) {
		t.Fatalf("error is %T, want *KeyTypeError", err)
	}
	if ktErr.Key != "id" {
		t.Fatalf("KeyTypeError.Key = %q, want %q", ktErr.Key, "id")
	}
	if ktErr.LeftType != "string" || ktErr.RightType != "int64" {
		t.Fatalf("types = (%s, %s), want (string, int64)", ktErr.LeftType, ktErr.RightType)
	}
}

// Unsupported key types are a decidable error, not a panic.
func TestUnsupportedKeyTypeError(t *testing.T) {
	left := []Row{{"id": []int{1}}}
	right := []Row{{"id": []int{1}}}
	_, _, err := Join(left, right, []string{"id"}, Inner)
	if err == nil {
		t.Fatal("expected error for unsupported key type, got nil")
	}
}

// int64 and float64 keys compare by numeric value.
func TestInt64Float64CrossCompare(t *testing.T) {
	left := []Row{{"id": int64(5), "tag": "L"}}
	right := []Row{{"id": 5.0, "w": "R"}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("int64(5) vs float64(5.0) matched %d rows, want 1", len(out))
	}
}

// NaN never equals NaN on a join key.
func TestNaNNeverEqual(t *testing.T) {
	left := []Row{{"id": math.NaN()}}
	right := []Row{{"id": math.NaN()}}
	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("NaN matched %d rows, want 0", len(out))
	}
	if stats.LeftNullKey != 1 {
		t.Fatalf("LeftNullKey = %d, want 1", stats.LeftNullKey)
	}
}

// Positive and negative zero are equal join keys.
func TestSignedZeroEqual(t *testing.T) {
	left := []Row{{"id": 0.0, "tag": "L"}}
	right := []Row{{"id": math.Copysign(0, -1), "w": "R"}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("+0.0 vs -0.0 matched %d rows, want 1", len(out))
	}
}

// Plain int literals (the common case in Go maps) join with int64 values.
func TestIntAndInt64Compatible(t *testing.T) {
	left := []Row{{"id": 5, "tag": "L"}}
	right := []Row{{"id": int64(5), "w": "R"}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("int(5) vs int64(5) matched %d rows, want 1", len(out))
	}
}

// String and bool keys work within their own category.
func TestStringAndBoolKeys(t *testing.T) {
	left := []Row{{"s": "x", "b": true}}
	right := []Row{{"s": "x", "b": true}, {"s": "x", "b": false}}
	out, _, err := Join(left, right, []string{"s", "b"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
}
