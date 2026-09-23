package vec

import (
	"errors"
	"math"
	"testing"
)

func TestVecOps(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Vector
		wantDot float64
		wantEu  float64
		wantErr error
	}{
		{"aligned", Vector{1, 2, 3}, Vector{4, 5, 6}, 32, math.Sqrt(27), nil},
		{"orthogonal", Vector{1, 0}, Vector{0, 1}, 0, math.Sqrt2, nil},
		{"negatives", Vector{-1, -2}, Vector{3, 1}, -5, math.Sqrt(25), nil},
		{"dim1", Vector{3}, Vector{1}, 3, 2, nil},
		{"zero", Vector{0, 0}, Vector{0, 0}, 0, 0, nil},
		{"mismatch", Vector{1, 2}, Vector{1}, 0, 0, ErrDimMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dot, err := Dot(tt.a, tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Dot err=%v want %v", err, tt.wantErr)
			}
			if err == nil && math.Abs(dot-tt.wantDot) > 1e-12 {
				t.Fatalf("Dot=%v want %v", dot, tt.wantDot)
			}
			eu, err := Euclidean(tt.a, tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Euclidean err=%v want %v", err, tt.wantErr)
			}
			if err == nil && math.Abs(eu-tt.wantEu) > 1e-12 {
				t.Fatalf("Euclidean=%v want %v", eu, tt.wantEu)
			}
			sq, err := EuclideanSq(tt.a, tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("EuclideanSq err=%v want %v", err, tt.wantErr)
			}
			if err == nil && math.Abs(sq-tt.wantEu*tt.wantEu) > 1e-12 {
				t.Fatalf("EuclideanSq=%v want %v", sq, tt.wantEu*tt.wantEu)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		v    Vector
		nan  bool
		inf  bool
		ok   bool
	}{
		{"clean", Vector{1, 2}, false, false, true},
		{"zero", Vector{0}, false, false, true},
		{"nan", Vector{1, math.NaN()}, true, false, false},
		{"posinf", Vector{math.Inf(1)}, false, true, false},
		{"neginf", Vector{math.Inf(-1)}, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasNaN(tt.v); got != tt.nan {
				t.Fatalf("HasNaN=%v want %v", got, tt.nan)
			}
			if got := HasInf(tt.v); got != tt.inf {
				t.Fatalf("HasInf=%v want %v", got, tt.inf)
			}
			err := Validate(tt.v)
			if (err == nil) != tt.ok {
				t.Fatalf("Validate err=%v ok=%v", err, tt.ok)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("Validate err=%v want ErrInvalidValue", err)
			}
		})
	}
}

func TestCheckDim(t *testing.T) {
	if err := CheckDim(3, 3); err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if err := CheckDim(3, 2); !errors.Is(err, ErrDimMismatch) {
		t.Fatalf("err=%v want ErrDimMismatch", err)
	}
}
