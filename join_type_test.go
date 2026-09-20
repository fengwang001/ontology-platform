package ontology

import (
	"errors"
	"math"
	"testing"
)

// A key column typed string on the left and int64 on the right is a
// decidable error naming the key and both types, not a silent mismatch.
func TestKeyTypeConflictError(t *testing.T) {
	left := []map[string]any{{"k": "1", "v": "l"}}
	right := []map[string]any{{"k": int64(1), "w": "r"}}
	_, err := Join(left, right, []string{"k"}, Inner)
	if err == nil {
		t.Fatal("expected a KeyTypeError, got nil")
	}
	var kte *KeyTypeError
	if !errors.As(err, &kte) {
		t.Fatalf("error %v is not a *KeyTypeError", err)
	}
	if kte.Key != "k" {
		t.Fatalf("KeyTypeError.Key = %q, want %q", kte.Key, "k")
	}
	if kte.LeftType != "string" || kte.RightType != "int64" {
		t.Fatalf("types = (%s, %s), want (string, int64)", kte.LeftType, kte.RightType)
	}
}

// Mixed families within one side are also a decidable error.
func TestKeyTypeConflictWithinOneSide(t *testing.T) {
	left := []map[string]any{{"k": "a"}, {"k": true}}
	right := []map[string]any{{"k": "a"}}
	_, err := Join(left, right, []string{"k"}, Inner)
	var kte *KeyTypeError
	if !errors.As(err, &kte) {
		t.Fatalf("expected *KeyTypeError, got %v", err)
	}
	if kte.Key != "k" {
		t.Fatalf("KeyTypeError.Key = %q, want %q", kte.Key, "k")
	}
}

// int64 and float64 compare by numeric value across the two sides.
func TestInt64Float64CrossTypeEquality(t *testing.T) {
	left := []map[string]any{
		{"k": int64(3), "v": "three"},
		{"k": float64(2.5), "v": "two-five"},
	}
	right := []map[string]any{
		{"k": float64(3.0), "w": "r3"},
		{"k": int64(25), "w": "r25"},
	}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 1 {
		t.Fatalf("RowCount = %d, want 1 (int64(3) == float64(3.0))", res.RowCount())
	}
	if res.Rows[0]["v"] != "three" || res.Rows[0]["w"] != "r3" {
		t.Fatalf("wrong pair matched: %v", res.Rows[0])
	}
}

// +0.0 and -0.0 are the same key.
func TestSignedZeroEquality(t *testing.T) {
	left := []map[string]any{{"k": 0.0, "v": "pos"}}
	right := []map[string]any{{"k": math.Copysign(0, -1), "w": "neg"}}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 1 {
		t.Fatalf("RowCount = %d, want 1 (+0.0 == -0.0)", res.RowCount())
	}
}

// NaN on either side never matches, even NaN vs NaN.
func TestNaNNeverEqualAcrossSides(t *testing.T) {
	left := []map[string]any{{"k": math.NaN(), "v": "l"}}
	right := []map[string]any{{"k": math.NaN(), "w": "r"}}
	res, err := Join(left, right, []string{"k"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 1 {
		t.Fatalf("RowCount = %d, want 1 (unmatched left row only)", res.RowCount())
	}
	if !IsMissing(res.Rows[0], "w") {
		t.Fatal("NaN left row should be unmatched")
	}
}

// Large int64 keys beyond float64 precision still compare exactly.
func TestLargeInt64ExactComparison(t *testing.T) {
	big := int64(1)<<53 + 1 // not representable as float64
	left := []map[string]any{{"k": big, "v": "l"}}
	right := []map[string]any{{"k": big, "w": "r"}}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 1 {
		t.Fatalf("RowCount = %d, want 1", res.RowCount())
	}
	// A float64 neighbor must not collide with the int64 key.
	right2 := []map[string]any{{"k": float64(int64(1) << 53), "w": "r"}}
	res2, err := Join(left, right2, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res2.RowCount() != 0 {
		t.Fatalf("float64(2^53) must not equal int64(2^53+1)")
	}
}
