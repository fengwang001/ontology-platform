package plan_test

import (
	"errors"
	"testing"

	"ontology/plan"
)

func stdRanges() []plan.Range {
	return []plan.Range{
		{
			Lo: 1, Hi: 50,
			Normal:    plan.Plan{N: 5, Ac: 0, Re: 1},
			Tightened: plan.Plan{N: 8, Ac: 0, Re: 1},
			Reduced:   plan.Plan{N: 3, Ac: 0, Re: 2},
		},
		{
			Lo: 51, Hi: 150,
			Normal:    plan.Plan{N: 13, Ac: 1, Re: 2},
			Tightened: plan.Plan{N: 20, Ac: 1, Re: 2},
			Reduced:   plan.Plan{N: 5, Ac: 0, Re: 2},
		},
	}
}

func TestNewTable(t *testing.T) {
	good := plan.Plan{N: 5, Ac: 0, Re: 1}
	cases := []struct {
		name   string
		ranges []plan.Range
		lr     int
		want   error
	}{
		{name: "ok", ranges: stdRanges(), lr: 2, want: nil},
		{name: "reduced 允许 Re>Ac+1", ranges: []plan.Range{
			{Lo: 1, Hi: 10, Normal: good, Tightened: good, Reduced: plan.Plan{N: 3, Ac: 1, Re: 4}},
		}, lr: 0, want: nil},
		{name: "空区间表", ranges: nil, lr: 0, want: plan.ErrInvalidParam},
		{name: "Lr 为负", ranges: stdRanges(), lr: -1, want: plan.ErrInvalidParam},
		{name: "lo 小于 1", ranges: []plan.Range{
			{Lo: 0, Hi: 10, Normal: good, Tightened: good, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "lo 大于 hi", ranges: []plan.Range{
			{Lo: 10, Hi: 5, Normal: good, Tightened: good, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "区间重叠", ranges: []plan.Range{
			{Lo: 1, Hi: 50, Normal: good, Tightened: good, Reduced: good},
			{Lo: 50, Hi: 90, Normal: good, Tightened: good, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "乱序区间重叠", ranges: []plan.Range{
			{Lo: 51, Hi: 90, Normal: good, Tightened: good, Reduced: good},
			{Lo: 1, Hi: 51, Normal: good, Tightened: good, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "n 为 0", ranges: []plan.Range{
			{Lo: 1, Hi: 10, Normal: plan.Plan{N: 0, Ac: 0, Re: 1}, Tightened: good, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "Ac 为负", ranges: []plan.Range{
			{Lo: 1, Hi: 10, Normal: good, Tightened: plan.Plan{N: 5, Ac: -1, Re: 1}, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "Ac 等于 Re", ranges: []plan.Range{
			{Lo: 1, Hi: 10, Normal: good, Tightened: good, Reduced: plan.Plan{N: 5, Ac: 1, Re: 1}},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "Ac 大于 Re", ranges: []plan.Range{
			{Lo: 1, Hi: 10, Normal: plan.Plan{N: 5, Ac: 2, Re: 1}, Tightened: good, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "Normal Re 不等于 Ac+1", ranges: []plan.Range{
			{Lo: 1, Hi: 10, Normal: plan.Plan{N: 5, Ac: 0, Re: 2}, Tightened: good, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
		{name: "Tightened Re 不等于 Ac+1", ranges: []plan.Range{
			{Lo: 1, Hi: 10, Normal: good, Tightened: plan.Plan{N: 5, Ac: 1, Re: 3}, Reduced: good},
		}, lr: 0, want: plan.ErrInvalidParam},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := plan.NewTable(tc.ranges, tc.lr)
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewTable err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	tb, err := plan.NewTable(stdRanges(), 2)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		n      int
		wantOK bool
		wantLo int
	}{
		{n: 1, wantOK: true, wantLo: 1},
		{n: 50, wantOK: true, wantLo: 1},
		{n: 51, wantOK: true, wantLo: 51},
		{n: 150, wantOK: true, wantLo: 51},
		{n: 0, wantOK: false},
		{n: 151, wantOK: false},
		{n: 1_000_000, wantOK: false},
	}
	for _, tc := range cases {
		r, ok := tb.Lookup(tc.n)
		if ok != tc.wantOK {
			t.Fatalf("Lookup(%d) ok = %v, want %v", tc.n, ok, tc.wantOK)
		}
		if ok && r.Lo != tc.wantLo {
			t.Fatalf("Lookup(%d) lo = %d, want %d", tc.n, r.Lo, tc.wantLo)
		}
	}
}

func TestTablePlan(t *testing.T) {
	tb, err := plan.NewTable(stdRanges(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := tb.Plan(100, plan.Normal); !ok || p != (plan.Plan{N: 13, Ac: 1, Re: 2}) {
		t.Fatalf("Normal plan = %+v, %v", p, ok)
	}
	if p, ok := tb.Plan(100, plan.Tightened); !ok || p != (plan.Plan{N: 20, Ac: 1, Re: 2}) {
		t.Fatalf("Tightened plan = %+v, %v", p, ok)
	}
	if p, ok := tb.Plan(100, plan.Reduced); !ok || p != (plan.Plan{N: 5, Ac: 0, Re: 2}) {
		t.Fatalf("Reduced plan = %+v, %v", p, ok)
	}
	if _, ok := tb.Plan(100, plan.Suspended); ok {
		t.Fatal("Suspended 不应有方案")
	}
	if _, ok := tb.Plan(500, plan.Normal); ok {
		t.Fatal("无区间的批量不应有方案")
	}
	if tb.Lr() != 2 {
		t.Fatalf("Lr = %d, want 2", tb.Lr())
	}
}
