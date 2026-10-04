package pool

import (
	"errors"
	"testing"
)

func TestPool(t *testing.T) {
	tests := []struct {
		name      string
		h         int
		prepare   func(*Pool)
		preferred int
		hero      int
		ok        bool
		maxTouch  int
	}{
		{
			name:      "preferred available",
			h:         65536,
			preferred: 9,
			hero:      9,
			ok:        true,
			maxTouch:  1,
		},
		{
			name: "preferred banned falls back to smallest",
			h:    65536,
			prepare: func(p *Pool) {
				p.Ban(1, 1)
				p.Pick(2, 2, 2)
				p.Ban(3, 2)
				p.Ban(5, 1)
				p.Pick(7, 2, 3)
			},
			preferred: 5,
			hero:      4,
			ok:        true,
			maxTouch:  6,
		},
		{
			name: "scan stops at first gap rather than h",
			h:    64,
			prepare: func(p *Pool) {
				p.Ban(1, 1)
				p.Pick(2, 2, 2)
			},
			preferred: 1,
			hero:      3,
			ok:        true,
			maxTouch:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.h)
			if tt.prepare != nil {
				tt.prepare(p)
			}
			hero, touched, ok := p.Choose(tt.preferred)
			t.Logf("input h=%d preferred=%d blocked=%v output hero=%d touched=%d ok=%v; reason bounded ordered lookup", tt.h, tt.preferred, p.blocked, hero, touched, ok)
			if hero != tt.hero || ok != tt.ok || touched > tt.maxTouch || touched > 40 {
				t.Fatalf("Choose = (%d,%d,%v), want hero %d ok %v touch <= %d and <=40", hero, touched, ok, tt.hero, tt.ok, tt.maxTouch)
			}
		})
	}
}

func TestMutationsAndSnapshot(t *testing.T) {
	p := New(10)
	tests := []struct {
		name string
		call func() error
		want error
		used bool
	}{
		{"ban valid", func() error { return p.Ban(4, 1) }, nil, true},
		{"ban duplicate", func() error { return p.Ban(4, 2) }, ErrUnavailable, true},
		{"pick invalid", func() error { return p.Pick(11, 1, 0) }, ErrInvalidArgument, false},
		{"pick valid", func() error { return p.Pick(2, 2, 2) }, nil, true},
		{"pick duplicate", func() error { return p.Pick(2, 1, 0) }, ErrUnavailable, true},
	}
	for _, tt := range tests {
		err := tt.call()
		t.Logf("input operation=%s output error=%v; reason unique hero and range checks", tt.name, err)
		if !errors.Is(err, tt.want) {
			t.Fatalf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}

	snapshot := p.Clone()
	p.Ban(9, 2)
	p.Restore(snapshot)
	if p.Used(9) {
		t.Fatalf("input ban hero=9 output used=true after restore expected=false; reason snapshot rollback")
	}
	if !p.Used(4) || !p.Used(2) {
		t.Fatalf("output existing records missing after restore; reason snapshot keeps accepted state")
	}
}
