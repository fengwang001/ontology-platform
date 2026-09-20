package join

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestKeyTypeConflictIsDecidable(t *testing.T) {
	left := []Row{{"id": "abc"}}
	right := []Row{{"id": int64(1)}}
	_, err := Join(left, right, []string{"id"}, Inner)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrKeyTypeConflict) {
		t.Fatalf("errors.Is(err, ErrKeyTypeConflict) = false, err = %v", err)
	}
	var kte *KeyTypeError
	if !errors.As(err, &kte) {
		t.Fatalf("error is not *KeyTypeError: %T", err)
	}
	if kte.Key != "id" || kte.LeftType != "string" || kte.RightType != "int64" {
		t.Fatalf("KeyTypeError = %+v, want key=id left=string right=int64", kte)
	}
	msg := err.Error()
	for _, want := range []string{"id", "string", "int64"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not mention %q", msg, want)
		}
	}
}

func TestInt64AndFloat64AreComparable(t *testing.T) {
	left := []Row{{"id": int64(42), "v": "l"}}
	right := []Row{{"id": 42.0, "w": "r"}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (int64 42 == float64 42.0)", len(res.Rows))
	}
	// 数值不等则不应匹配
	res2, err := Join([]Row{{"id": int64(42)}}, []Row{{"id": 42.5}}, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Rows) != 0 {
		t.Fatalf("rows = %d, want 0 (42 != 42.5)", len(res2.Rows))
	}
}

func TestNaNNeverEquals(t *testing.T) {
	left := []Row{{"id": math.NaN()}}
	right := []Row{{"id": math.NaN()}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("NaN matched NaN: %v", res.Rows)
	}
}

func TestPlusMinusZeroAreEqual(t *testing.T) {
	left := []Row{{"id": 0.0, "v": "l"}}
	right := []Row{{"id": math.Copysign(0, -1), "w": "r"}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (+0.0 == -0.0)", len(res.Rows))
	}
	// -0.0 与 int64 0 也相等
	res2, err := Join([]Row{{"id": math.Copysign(0, -1)}}, []Row{{"id": int64(0)}}, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (-0.0 == int64 0)", len(res2.Rows))
	}
}

func TestUnsupportedKeyType(t *testing.T) {
	left := []Row{{"id": []byte("x")}}
	right := []Row{{"id": []byte("x")}}
	_, err := Join(left, right, []string{"id"}, Inner)
	if !errors.Is(err, ErrUnsupportedKeyType) {
		t.Fatalf("err = %v, want ErrUnsupportedKeyType", err)
	}
}

func TestSameSideMixedTypesConflict(t *testing.T) {
	left := []Row{{"id": "a"}, {"id": int64(1)}}
	right := []Row{{"id": "a"}}
	_, err := Join(left, right, []string{"id"}, Inner)
	if !errors.Is(err, ErrKeyTypeConflict) {
		t.Fatalf("err = %v, want ErrKeyTypeConflict", err)
	}
}

func TestNoKeysError(t *testing.T) {
	_, err := Join([]Row{{"id": 1}}, []Row{{"id": 1}}, nil, Inner)
	if !errors.Is(err, ErrNoKeys) {
		t.Fatalf("err = %v, want ErrNoKeys", err)
	}
}
