package join_test

import (
	"errors"
	"math"
	"testing"

	"ontology/join"
)

func TestTypeConflictCross(t *testing.T) {
	left := []join.Row{{"id": "a"}}
	right := []join.Row{{"id": int64(1)}}
	_, err := join.Join(left, right, []string{"id"}, join.Inner)
	var te *join.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("want *TypeError, got %v", err)
	}
	if te.Key != "id" || te.Side != "cross" {
		t.Fatalf("unexpected TypeError: %+v", te)
	}
	if te.TypeA != "string" || te.TypeB != "int64" {
		t.Fatalf("want string vs int64, got %s vs %s", te.TypeA, te.TypeB)
	}
}

func TestTypeConflictWithinSide(t *testing.T) {
	left := []join.Row{{"id": "a"}, {"id": int64(1)}}
	right := []join.Row{{"id": "a"}}
	_, err := join.Join(left, right, []string{"id"}, join.Inner)
	var te *join.TypeError
	if !errors.As(err, &te) || te.Side != "left" {
		t.Fatalf("want left-side TypeError, got %v", err)
	}
}

func TestUnsupportedKeyType(t *testing.T) {
	left := []join.Row{{"id": struct{}{}}}
	right := []join.Row{{"id": int64(1)}}
	_, err := join.Join(left, right, []string{"id"}, join.Inner)
	var ute *join.UnsupportedTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("want *UnsupportedTypeError, got %v", err)
	}
	if ute.Key != "id" || ute.Side != "left" {
		t.Fatalf("unexpected UnsupportedTypeError: %+v", ute)
	}
}

func TestInt64Float64Equal(t *testing.T) {
	left := []join.Row{{"id": int64(2), "lv": "L"}}
	right := []join.Row{{"id": float64(2.0), "rv": "R"}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.MatchedRows != 1 || len(res.Rows) != 1 {
		t.Fatalf("want 1 matched row, got %+v", res.Stats)
	}
	if res.Rows[0]["lv"] != "L" || res.Rows[0]["rv"] != "R" {
		t.Fatalf("bad merged row: %v", res.Rows[0])
	}
}

func TestLargeInt64NotLostInFloat(t *testing.T) {
	// 2^53+1 无法被 float64 精确表示，不应与 float64(2^53) 匹配。
	left := []join.Row{{"id": int64(9007199254740993)}}
	right := []join.Row{{"id": float64(9007199254740992)}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.MatchedRows != 0 {
		t.Fatalf("precision loss: got %d matches", res.Stats.MatchedRows)
	}
}

func TestNaNNeverEqual(t *testing.T) {
	nan := math.NaN()
	left := []join.Row{{"id": nan}, {"id": 1.0}}
	right := []join.Row{{"id": nan}, {"id": int64(1)}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.MatchedRows != 1 {
		t.Fatalf("NaN must not match NaN, matched=%d", res.Stats.MatchedRows)
	}
	if res.Stats.LeftNullKeyRows != 1 || res.Stats.RightNullKeyRows != 1 {
		t.Fatalf("NaN should count as null key: %+v", res.Stats)
	}
	if res.Stats.LeftUnmatchedRows != 0 {
		t.Fatalf("NaN row must not count as value-unmatched: %+v", res.Stats)
	}
}

func TestSignedZeroEqual(t *testing.T) {
	neg := math.Copysign(0, -1)
	left := []join.Row{{"id": 0.0}}
	right := []join.Row{{"id": neg}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.MatchedRows != 1 {
		t.Fatalf("+0.0 and -0.0 must be equal, matched=%d", res.Stats.MatchedRows)
	}
}

func TestMultiKeyAndBoolStringKeys(t *testing.T) {
	left := []join.Row{
		{"a": true, "b": "x", "lv": 1},
		{"a": true, "b": "y", "lv": 2},
	}
	right := []join.Row{
		{"a": true, "b": "x", "rv": "hit"},
	}
	res, err := join.Join(left, right, []string{"a", "b"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.MatchedRows != 1 || res.Rows[0]["lv"] != 1 {
		t.Fatalf("multi-key match failed: %+v rows=%v", res.Stats, res.Rows)
	}
}

func TestNoKeysAndInvalidMode(t *testing.T) {
	rows := []join.Row{{"id": int64(1)}}
	if _, err := join.Join(rows, rows, nil, join.Inner); !errors.Is(err, join.ErrNoKeys) {
		t.Fatalf("want ErrNoKeys, got %v", err)
	}
	if _, err := join.Join(rows, rows, []string{"id"}, join.Mode(9)); !errors.Is(err, join.ErrInvalidMode) {
		t.Fatalf("want ErrInvalidMode, got %v", err)
	}
}
