package ontology

import (
	"errors"
	"math"
	"testing"
)

// 同名键左右类型不可比较时返回可判定的 *KeyTypeError，指出键与两侧类型。
func TestKeyTypeConflict(t *testing.T) {
	left := []map[string]any{{"id": "1"}}
	right := []map[string]any{{"id": int64(1)}}
	_, _, err := Join(left, right, []string{"id"}, Inner)
	var kte *KeyTypeError
	if !errors.As(err, &kte) {
		t.Fatalf("err = %v, want *KeyTypeError", err)
	}
	if kte.Key != "id" || kte.LeftType != "string" || kte.RightType != "int64" {
		t.Fatalf("KeyTypeError = %+v", kte)
	}
	// bool 对 string 同样冲突。
	_, _, err = Join(
		[]map[string]any{{"id": true}},
		[]map[string]any{{"id": "x"}}, []string{"id"}, Inner)
	if !errors.As(err, &kte) {
		t.Fatalf("err = %v, want *KeyTypeError", err)
	}
}

// 不支持的键类型返回可判定的 *UnsupportedKeyTypeError。
func TestUnsupportedKeyType(t *testing.T) {
	left := []map[string]any{{"id": []byte("a")}}
	_, _, err := Join(left, nil, []string{"id"}, Inner)
	var ute *UnsupportedKeyTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("err = %v, want *UnsupportedKeyTypeError", err)
	}
	if ute.Key != "id" || ute.Side != "left" || ute.Type != "[]uint8" {
		t.Fatalf("UnsupportedKeyTypeError = %+v", ute)
	}
}

// int64 与 float64 可比并按数值相等判断。
func TestInt64Float64Equality(t *testing.T) {
	left := []map[string]any{
		{"id": int64(42), "v": "a"},
		{"id": int64(7), "v": "b"},
	}
	right := []map[string]any{
		{"id": 42.0, "w": "x"},
		{"id": 7.5, "w": "y"}, // 7.5 != 7
	}
	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 || out[0]["v"] != "a" || out[0]["right.w"] != "x" {
		t.Fatalf("rows = %v", out)
	}
	if stats.LeftUnmatchedRows != 1 || stats.RightUnmatchedRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	// int 与 int64、float64 也可比。
	out, _, err = Join(
		[]map[string]any{{"id": 5}},
		[]map[string]any{{"id": int64(5)}}, []string{"id"}, Inner)
	if err != nil || len(out) != 1 {
		t.Fatalf("int vs int64: rows=%d err=%v", len(out), err)
	}
}

// NaN 作为连接键永不相等，且计入"键为空"的未匹配计数。
func TestNaNNeverEquals(t *testing.T) {
	nan := math.NaN()
	left := []map[string]any{{"id": nan, "v": "l"}}
	right := []map[string]any{{"id": nan, "w": "r"}}
	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("NaN matched: %v", out)
	}
	if stats.LeftNullKeyRows != 1 || stats.LeftUnmatchedRows != 0 {
		t.Fatalf("stats = %+v, want null=1 unmatched=0", stats)
	}
	// Left 下 NaN 左行作为未匹配行输出。
	out, _, err = Join(left, right, []string{"id"}, Left)
	if err != nil || len(out) != 1 {
		t.Fatalf("Left with NaN: rows=%d err=%v", len(out), err)
	}
}

// +0.0 与 -0.0 视为相等；0.0 与 int64(0) 也相等。
func TestSignedZeroEquality(t *testing.T) {
	pos, neg := 0.0, math.Copysign(0, -1)
	left := []map[string]any{{"id": pos, "v": "l"}}
	right := []map[string]any{{"id": neg, "w": "r"}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("+0.0 vs -0.0 not equal: %v", out)
	}
	out, _, err = Join(
		[]map[string]any{{"id": int64(0)}},
		[]map[string]any{{"id": neg}}, []string{"id"}, Inner)
	if err != nil || len(out) != 1 {
		t.Fatalf("int64(0) vs -0.0: rows=%d err=%v", len(out), err)
	}
}

// 大整数：超出 float64 精确范围的 int64 不与近似 float64 相等。
func TestLargeInt64NotEqualRoundedFloat(t *testing.T) {
	big := int64(9007199254740993) // 2^53+1，float64 无法精确表示
	left := []map[string]any{{"id": big}}
	right := []map[string]any{{"id": float64(9007199254740992)}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("2^53+1 wrongly matched rounded float")
	}
}
