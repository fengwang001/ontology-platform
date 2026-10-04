package pool

import (
	"errors"
	"testing"
)

func TestLifecycle(t *testing.T) {
	tests := []struct {
		name string
		m    int
		nmax int
		ops  func(p *Pool) error
		want error
	}{
		{"新增成功", 2, 3, func(p *Pool) error { return p.Add("a") }, nil},
		{"重复新增报已存在", 2, 3, func(p *Pool) error {
			p.Add("a")
			return p.Add("a")
		}, ErrExists},
		{"达Nmax报已满", 2, 1, func(p *Pool) error {
			p.Add("a")
			return p.Add("b")
		}, ErrFull},
		{"移除不存在报未找到", 2, 3, func(p *Pool) error { return p.Remove("x") }, ErrNotFound},
		{"在途为零立即移除", 2, 3, func(p *Pool) error {
			p.Add("a")
			return p.Remove("a")
		}, nil},
		{"有在途转排空", 2, 3, func(p *Pool) error {
			p.Add("a")
			p.Get("a").Inflight = 1
			return p.Remove("a")
		}, nil},
		{"排空中再移除报排空中", 2, 3, func(p *Pool) error {
			p.Add("a")
			p.Get("a").Inflight = 1
			p.Remove("a")
			return p.Remove("a")
		}, ErrDraining},
		{"排空中重复新增报已存在", 2, 3, func(p *Pool) error {
			p.Add("a")
			p.Get("a").Inflight = 1
			p.Remove("a")
			return p.Add("a")
		}, ErrExists},
		{"排空中占用Nmax名额", 2, 1, func(p *Pool) error {
			p.Add("a")
			p.Get("a").Inflight = 1
			p.Remove("a")
			return p.Add("b")
		}, ErrFull},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := New(tc.m, tc.nmax)
			if err := tc.ops(p); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEligibleSortedAndFiltered(t *testing.T) {
	p := New(2, 10)
	for _, id := range []string{"c", "a", "b", "d"} {
		if err := p.Add(id); err != nil {
			t.Fatal(err)
		}
	}
	p.Get("c").Inflight = 2 // 满员，不入候选
	p.Get("d").Draining = true
	p.Get("d").Inflight = 1 // 排空，不入候选
	elig := p.Eligible()
	if len(elig) != 2 || elig[0].ID != "a" || elig[1].ID != "b" {
		t.Fatalf("Eligible = %v, want [a b]", elig)
	}
	if !p.HasActive() {
		t.Fatal("HasActive 应为 true")
	}
}

func TestReleaseOneRemovesDrainedEmpty(t *testing.T) {
	p := New(2, 2)
	p.Add("a")
	p.Get("a").Inflight = 1
	if err := p.Remove("a"); err != nil {
		t.Fatal(err)
	}
	p.ReleaseOne(p.Get("a"))
	if p.Get("a") != nil || p.Len() != 0 {
		t.Fatal("排空中端点在途归零后应被移除")
	}
	if p.HasActive() {
		t.Fatal("全部移除后 HasActive 应为 false")
	}
}
