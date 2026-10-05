package coerce_test

import (
	"errors"
	"math"
	"testing"

	"ontology/coerce"
	"ontology/mapping"
)

func TestLong(t *testing.T) {
	cases := []struct {
		in   any
		want int64
		ok   bool
	}{
		{int64(5), 5, true},
		{int64(math.MinInt64), math.MinInt64, true},
		{2.0, 2, true},
		{-2.0, -2, true},
		{0.0, 0, true},
		{2.5, 0, false},
		{-2.5, 0, false},
		{math.NaN(), 0, false},
		{math.Inf(1), 0, false},
		{math.Inf(-1), 0, false},
		{9.223372036854776e18, 0, false},             // 2^63
		{-9.223372036854776e18, math.MinInt64, true}, // -2^63
		{"0", 0, true},
		{"-7", -7, true},
		{"123", 123, true},
		{"9223372036854775807", math.MaxInt64, true},
		{"-9223372036854775808", math.MinInt64, true},
		{"9223372036854775808", 0, false},
		{"-9223372036854775809", 0, false},
		{"-0", 0, false},
		{"07", 0, false},
		{"00", 0, false},
		{"-07", 0, false},
		{"+7", 0, false},
		{" 7", 0, false},
		{"7 ", 0, false},
		{"", 0, false},
		{"-", 0, false},
		{"1.0", 0, false},
		{"abc", 0, false},
		{true, 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, err := coerce.Long(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("Long(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("Long(%v) 应拒绝", c.in)
		}
		if !c.ok && err != nil && !isConflict(err) {
			t.Errorf("Long(%v) 错误类别应为类型冲突: %v", c.in, err)
		}
	}
}

func TestDouble(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{2.5, 2.5, true},
		{0.0, 0.0, true},
		{int64(1) << 53, 1 << 53, true},
		{int64(-1) << 53, -1 << 53, true},
		{int64(1)<<53 + 1, 0, false},
		{int64(-1)<<53 - 1, 0, false},
		{int64(math.MaxInt64), 0, false},
		{math.NaN(), 0, false},
		{math.Inf(1), 0, false},
		{math.Inf(-1), 0, false},
		{"1.5", 0, false},
		{true, 0, false},
	}
	for _, c := range cases {
		got, err := coerce.Double(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("Double(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("Double(%v) 应拒绝", c.in)
		}
	}
}

func TestKeyword(t *testing.T) {
	cases := []struct {
		in   any
		want string
		ok   bool
	}{
		{"abc", "abc", true},
		{"", "", true},
		{int64(12), "12", true},
		{int64(-3), "-3", true},
		{int64(0), "0", true},
		{true, "true", true},
		{false, "false", true},
		{2.5, "", false},
		{2.0, "", false},
	}
	for _, c := range cases {
		got, err := coerce.Keyword(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("Keyword(%v) = %q, %v; want %q", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("Keyword(%v) 应拒绝", c.in)
		}
	}
}

func TestBool(t *testing.T) {
	cases := []struct {
		in   any
		want bool
		ok   bool
	}{
		{true, true, true},
		{false, false, true},
		{"true", true, true},
		{"false", false, true},
		{"True", false, false},
		{"FALSE", false, false},
		{"1", false, false},
		{"", false, false},
		{int64(1), false, false},
		{1.0, false, false},
	}
	for _, c := range cases {
		got, err := coerce.Bool(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("Bool(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("Bool(%v) 应拒绝", c.in)
		}
	}
}

func TestCoerceObjectRejected(t *testing.T) {
	if _, err := coerce.Coerce(mapping.Object, map[string]any{}); !isConflict(err) {
		t.Fatalf("object 强转应报类型冲突, got %v", err)
	}
}

func isConflict(err error) bool {
	return errors.Is(err, mapping.ErrTypeConflict)
}
