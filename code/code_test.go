package code

import (
	"errors"
	"testing"
)

func TestValidPrograms(t *testing.T) {
	ok := []Code{
		{Step([]byte("a"))},
		{
			Step([]byte("a")),
			Branch([]byte("p"), []Item{Step([]byte("b2"))}, []Item{Step([]byte("b"))}),
			Step([]byte("c")),
		},
		{Branch([]byte("p"), nil, nil)}, // 空 N 与空 O
	}
	for i, c := range ok {
		if err := c.Validate(); err != nil {
			t.Fatalf("case %d: expected valid, got %v", i, err)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	stepN := func(n int) []Item {
		out := make([]Item, n)
		for i := range out {
			out[i] = Step([]byte("s"))
		}
		return out
	}

	tests := []struct {
		name string
		c    Code
		want error
	}{
		{"empty program", Code{}, ErrArgument},
		{"too many top items", append(Code{}, stepN(MaxItems+1)...), ErrArgument},
		{"empty step name", Code{Step(nil)}, ErrArgument},
		{"empty branch pid", Code{Branch(nil, nil, nil)}, ErrArgument},
		{"unknown item kind", Code{{Kind: 42}}, ErrArgument},
		{
			"nested branch in N",
			Code{Branch([]byte("p"), []Item{Branch([]byte("q"), nil, nil)}, nil)},
			ErrCode,
		},
		{
			"nested branch in O",
			Code{Branch([]byte("p"), nil, []Item{Branch([]byte("q"), nil, nil)})},
			ErrCode,
		},
		{
			"empty step name inside O",
			Code{Branch([]byte("p"), nil, []Item{Step(nil)})},
			ErrArgument,
		},
		{
			"too many N steps",
			Code{Branch([]byte("p"), stepN(MaxBranchItems+1), nil)},
			ErrArgument,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}

	// 参数非法优先于嵌套 ErrCode：顶层空 name 与嵌套同时存在时先报参数。
	mixed := Code{
		Step(nil),
		Branch([]byte("p"), []Item{Branch([]byte("q"), nil, nil)}, nil),
	}
	if err := mixed.Validate(); !errors.Is(err, ErrArgument) {
		t.Fatalf("argument must precede ErrCode, got %v", err)
	}
}
