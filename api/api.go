// Package api is the external entry point (rfr + independent recompute + SelfCheck).
package api

import (
	"errors"
	"maps"

	"ontology/rfr"
)

// Four pairwise-distinct decidable failure classes, re-exported from rfr.
var (
	ErrEmptyName    = rfr.ErrEmptyName
	ErrUnknownName  = rfr.ErrUnknownName
	ErrCycle        = rfr.ErrCycle
	ErrTooManyViews = rfr.ErrTooManyViews
)

// Change is one -(name,old)/+(name,new) pair in refresh order.
type Change = rfr.Change

type API struct{ r *rfr.Refresher }

func New(maxViews int) *API { return &API{r: rfr.New(maxViews)} }

func (a *API) AddView(name string, deps ...string) error { return a.r.AddView(name, deps) }
func (a *API) SetBase(name string, v int64) error        { return a.r.SetBase(name, v) }
func (a *API) Refresh() ([]Change, error)                { return a.r.Refresh(), nil }
func (a *API) View() map[string]int64                    { return a.r.Views() }
func (a *API) snapshot() rfr.Snapshot                    { return a.r.Snapshot() }

// fullRecompute derives all view values from base ends by memoized DFS
// without reading maintained values, cross-checking invariant 1.
func fullRecompute(s rfr.Snapshot) map[string]int64 {
	got := map[string]int64{}
	var val func(string) int64
	val = func(v string) int64 {
		if x, ok := got[v]; ok {
			return x
		}
		var sum int64
		for _, d := range s.Deps[v] {
			if _, isView := s.Deps[d]; isView {
				sum += val(d)
			} else {
				sum += s.Bases[d] // a never-set base contributes its initial 0
			}
		}
		got[v] = sum
		return sum
	}
	for _, v := range s.Views {
		val(v)
	}
	return got
}

var errSC = errors.New

// SelfCheck replays the section-3 batch (invariants 1-3) and all four rejections.
func (a *API) SelfCheck() error {
	c := New(0)
	for _, d := range [][]string{
		{"v1", "b1"}, {"v2", "b2"}, {"v3", "v1", "v2"}, {"v4", "v3", "b3"},
	} {
		if err := c.AddView(d[0], d[1:]...); err != nil {
			return err
		}
	}
	bs := []string{"b1", "b3", "b2", "b1", "b2", "b3", "b1", "b2"}
	xs := []int64{2, 7, 4, 5, 1, 2, 3, 6}
	for i := range bs {
		if err := c.SetBase(bs[i], xs[i]); err != nil {
			return err
		}
	}
	log, err := c.Refresh()
	if err != nil {
		return err
	}
	want := []Change{
		{Name: "v1", Old: 0, New: 3}, {Name: "v2", Old: 0, New: 6},
		{Name: "v3", Old: 0, New: 9}, {Name: "v4", Old: 0, New: 11},
	}
	for i := range want {
		if log[i] != want[i] {
			return errSC("SelfCheck: section-3 log mismatch")
		}
	}
	s := c.snapshot()
	if !maps.Equal(c.View(), fullRecompute(s)) {
		return errSC("SelfCheck: invariant 1 mismatch")
	}
	pos := map[string]int{}
	for i, x := range log {
		if _, dup := pos[x.Name]; dup {
			return errSC("SelfCheck: a view logged more than once")
		}
		pos[x.Name] = i
	}
	for _, x := range log {
		for _, d := range s.Deps[x.Name] {
			if j, dirty := pos[d]; dirty && j > pos[x.Name] {
				return errSC("SelfCheck: log order not topological")
			}
		}
	}
	return checkRejectionsNoTrace()
}

// checkRejectionsNoTrace pins invariant 4: rejected ops leave no trace.
func checkRejectionsNoTrace() error {
	full := func() *API { // quota exhausted: a third view hits ErrTooManyViews
		c := New(2)
		_ = c.AddView("v1", "b1")
		_ = c.AddView("v2", "b2")
		return c
	}
	room := func() *API { c := New(3); _ = c.AddView("v1", "b1"); return c }
	cases := []struct {
		build func() *API
		op    func(c *API) error
		want  error
	}{
		{full, func(c *API) error { return c.AddView("", "b1") }, ErrEmptyName},
		{full, func(c *API) error { return c.SetBase("ghost", 1) }, ErrUnknownName},
		{room, func(c *API) error { return c.AddView("v3", "v3") }, ErrCycle},
		{full, func(c *API) error { return c.AddView("v9", "b1") }, ErrTooManyViews},
	}
	for _, t := range cases {
		c := t.build()
		before := c.View()
		if !errors.Is(t.op(c), t.want) {
			return errSC("SelfCheck: wrong rejection error")
		}
		if !maps.Equal(c.View(), before) {
			return errSC("SelfCheck: rejected operation left a trace")
		}
	}
	c := full()
	if err := c.SetBase("b1", 5); err != nil {
		return err
	}
	c.Refresh()
	if c.View()["v1"] != 5 {
		return errSC("SelfCheck: instance unusable after rejections")
	}
	return nil
}
