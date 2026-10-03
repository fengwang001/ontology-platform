package code

import (
	"errors"
	"fmt"
	"testing"
)

func TestValidate(t *testing.T) {
	steps := func(n int) []Item {
		seq := make([]Item, n)
		for i := range seq {
			seq[i] = Step{Name: []byte(fmt.Sprintf("s%d", i))}
		}
		return seq
	}
	cases := []struct {
		name string
		code Code
		ok   bool
	}{
		{"empty code", nil, false},
		{"single step", Code{Step{Name: []byte("a")}}, true},
		{"empty step name", Code{Step{}}, false},
		{"too many items", Code(steps(MaxItems + 1)), false},
		{"max items ok", Code(steps(MaxItems)), true},
		{"empty pid", Code{Branch{New: steps(1)}}, false},
		{"empty bodies ok", Code{Branch{Pid: []byte("p")}}, true},
		{"nested branch", Code{Branch{Pid: []byte("p"),
			New: []Item{Branch{Pid: []byte("q")}}}}, false},
		{"nested in old", Code{Branch{Pid: []byte("p"),
			Old: []Item{Branch{Pid: []byte("q")}}}}, false},
		{"branch body too large", Code{Branch{Pid: []byte("p"),
			New: steps(MaxBranchItems + 1)}}, false},
		{"branch body max ok", Code{Branch{Pid: []byte("p"),
			New: steps(MaxBranchItems), Old: steps(MaxBranchItems)}}, true},
		{"empty name in body", Code{Branch{Pid: []byte("p"),
			New: []Item{Step{}}}}, false},
	}
	for _, tc := range cases {
		err := Validate(tc.code)
		if tc.ok && err != nil {
			t.Fatalf("%s: got %v, want nil", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrCode) {
			t.Fatalf("%s: got %v, want errors.Is ErrCode", tc.name, err)
		}
		t.Logf("%s: 判定 err=%v（依据：项数 1..%d、N/O 各 <=%d、禁嵌套、非空 name/pid）",
			tc.name, err, MaxItems, MaxBranchItems)
	}
}
