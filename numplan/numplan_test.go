package numplan

import (
	"errors"
	"testing"
)

func TestAssignAndHome(t *testing.T) {
	cases := []struct {
		name   string
		exec   func(p *Plan) error
		query  func(p *Plan) (int, bool)
		wantOp int
		wantOK bool
	}{
		{
			name: "before nested block home is outer",
			exec: func(p *Plan) error {
				if err := p.AssignBlock("1380", 11, 1, 0); err != nil {
					return err
				}
				return p.AssignBlock("13805", 11, 2, 10)
			},
			query:  func(p *Plan) (int, bool) { op, ok, _ := p.HomeAt("13805001234", 9); return op, ok },
			wantOp: 1, wantOK: true,
		},
		{
			name:   "after nested block home is inner",
			query:  func(p *Plan) (int, bool) { op, ok, _ := p.HomeAt("13805001234", 10); return op, ok },
			wantOp: 2, wantOK: true,
		},
		{
			name:   "at exact eff time block applies",
			query:  func(p *Plan) (int, bool) { op, ok, _ := p.HomeAt("13805001234", 0); return op, ok },
			wantOp: 1, wantOK: true,
		},
		{
			name:   "unassigned number",
			query:  func(p *Plan) (int, bool) { op, ok, _ := p.HomeAt("13999000000", 100); return op, ok },
			wantOp: 0, wantOK: false,
		},
	}
	p := New()
	for _, tc := range cases {
		if tc.exec != nil {
			if err := tc.exec(p); err != nil {
				t.Fatalf("%s: setup: %v", tc.name, err)
			}
		}
		gotOp, gotOK := tc.query(p)
		if gotOp != tc.wantOp || gotOK != tc.wantOK {
			t.Errorf("%s: got (%d,%v) want (%d,%v)", tc.name, gotOp, gotOK, tc.wantOp, tc.wantOK)
		}
	}
}

func TestAssignBlockErrors(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		length int
		op     int
		now    int64
		setup  func(p *Plan)
		want   error
	}{
		{"short number prefix", "13", 4, 1, 0, nil, ErrInvalid},
		{"prefix longer than L", "123456", 5, 1, 0, nil, ErrInvalid},
		{"non digits", "13a05", 5, 1, 0, nil, ErrInvalid},
		{"op zero", "13805", 5, 0, 0, nil, ErrInvalid},
		{"op too big", "13805", 5, 10001, 0, nil, ErrInvalid},
		{"now negative", "13805", 5, 1, -1, nil, ErrInvalid},
		{"now too big", "13805", 5, 1, 1e12 + 1, nil, ErrInvalid},
		{"duplicate", "1380", 11, 2, 5, func(p *Plan) {
			if err := p.AssignBlock("1380", 11, 1, 0); err != nil {
				t.Fatal(err)
			}
		}, ErrBlockExists},
		{"clock backwards", "1381", 11, 1, 0, func(p *Plan) {
			if err := p.AssignBlock("1382", 11, 1, 10); err != nil {
				t.Fatal(err)
			}
		}, ErrClockBack},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New()
			if tc.setup != nil {
				tc.setup(p)
			}
			err := p.AssignBlock(tc.prefix, tc.length, tc.op, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestValidNumber(t *testing.T) {
	if !ValidNumber("12345") || !ValidNumber("123456789012345") {
		t.Fatal("valid numbers rejected")
	}
	for _, s := range []string{"1234", "1234567890123456", "1234a", ""} {
		if ValidNumber(s) {
			t.Fatalf("invalid number accepted: %q", s)
		}
	}
}
