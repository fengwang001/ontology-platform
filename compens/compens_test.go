package compens

import (
	"errors"
	"reflect"
	"testing"

	"ontology/step"
)

func mkSteps(n int, nilComp ...int) []step.Step {
	nilSet := map[int]bool{}
	for _, i := range nilComp {
		nilSet[i] = true
	}
	out := make([]step.Step, n)
	for i := range out {
		out[i] = step.Step{Key: string(rune('a' + i))}
		if !nilSet[i] {
			out[i].Compensate = func() error { return nil }
		}
	}
	return out
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name  string
		steps []step.Step
		want  error
	}{
		{"empty", nil, ErrNoSteps},
		{"dup", []step.Step{{Key: "k"}, {Key: "k"}}, ErrDuplicateKey},
		{"ok", []step.Step{{Key: "a"}, {Key: "b"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.steps); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func TestBuildPlan(t *testing.T) {
	steps := mkSteps(4, 3)
	cases := []struct {
		name      string
		succeeded map[int]bool
		wantOrder []int
		wantSkip  []int
		wantErr   error
	}{
		{"all succeeded", map[int]bool{0: true, 1: true, 2: true}, []int{2, 1, 0}, []int{3}, nil},
		{"partial", map[int]bool{0: true, 1: true}, []int{1, 0}, []int{2, 3}, nil},
		{"only first", map[int]bool{0: true}, []int{0}, []int{1, 2, 3}, nil},
		{"none", map[int]bool{}, nil, []int{0, 1, 2, 3}, nil},
		{"nil compensate on success", map[int]bool{3: true}, nil, nil, ErrNilCompensate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Build(steps, tc.succeeded)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			var got []int
			for _, it := range p.Items {
				got = append(got, it.Index)
			}
			if !reflect.DeepEqual(got, tc.wantOrder) {
				t.Fatalf("order=%v want %v", got, tc.wantOrder)
			}
			if !reflect.DeepEqual(p.Skipped, tc.wantSkip) {
				t.Fatalf("skipped=%v want %v", p.Skipped, tc.wantSkip)
			}
		})
	}
}
