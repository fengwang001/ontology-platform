package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/entry"
)

func fmtLog(l []entry.Entry) string {
	parts := make([]string, len(l))
	for i, e := range l {
		parts[i] = fmt.Sprintf("%d/%d/%s", e.Term, e.Index, e.Cmd)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// checkedOf 经反射读取 raft 的非导出计数器（不经过任何导出接口）。
func checkedOf(s *api.Server) int64 {
	v := reflect.ValueOf(s).Elem()
	return v.FieldByName("c").Elem().FieldByName("r").Index(int(v.FieldByName("id").Int())).FieldByName("checked").Int()
}
func mk(s *api.Server, term int, cmds ...string) {
	s.SetTerm(term)
	for _, c := range cmds {
		api.Append(s, c)
	}
}
func TestScriptSixSteps(t *testing.T) {
	sv := api.New()
	arc := func(l, f int, cmd string, p int) {
		api.Append(sv[l], cmd)
		api.Replicate(sv[l], sv[f], p)
		api.CommitIndex(sv[l])
	}
	steps := []struct {
		ops      func()
		lead, ci int
		logs     string
	}{
		{func() { sv[0].SetTerm(1); arc(0, 1, "a", 0) }, 0, 1, "[1/1/a];[1/1/a];[]"},
		{func() { api.Append(sv[0], "b"); api.Replicate(sv[0], sv[1], 1) }, 0, 1, "[1/1/a 1/2/b];[1/1/a 1/2/b];[]"},
		{func() { sv[2].SetTerm(2); api.Append(sv[2], "x") }, 2, 0, "[1/1/a 1/2/b];[1/1/a 1/2/b];[2/1/x]"},
		{func() { api.Append(sv[2], "y") }, 2, 0, "[1/1/a 1/2/b];[1/1/a 1/2/b];[2/1/x 2/2/y]"},
		{func() { sv[0].SetTerm(3); api.Replicate(sv[0], sv[1], 1); arc(0, 1, "z", 2) }, 0, 3, "[1/1/a 1/2/b 3/3/z];[1/1/a 1/2/b 3/3/z];[2/1/x 2/2/y]"},
		{func() { api.Replicate(sv[0], sv[2], 0) }, 0, 3, "[1/1/a 1/2/b 3/3/z];[1/1/a 1/2/b 3/3/z];[1/1/a 1/2/b 3/3/z]"},
	}
	for i, s := range steps {
		s.ops()
		got := fmtLog(api.Log(sv[0])) + ";" + fmtLog(api.Log(sv[1])) + ";" + fmtLog(api.Log(sv[2]))
		if got != s.logs || api.Committed(sv[s.lead]) != s.ci {
			t.Errorf("step%d got %s CI=%d, want %s CI=%d", i+1, got, api.Committed(sv[s.lead]), s.logs, s.ci)
		}
	}
}
func TestReplicateTruncate(t *testing.T) {
	cases := []struct {
		name, want         string
		fTerm, lTerm, prev int
		wantErr            error
		fCmds, lCmds       []string
	}{
		{"match truncate+append", "[1/1/a 1/2/b 1/3/d]", 1, 1, 2, nil, []string{"a", "b", "c"}, []string{"a", "b", "d"}},
		{"term mismatch", "[2/1/a]", 2, 1, 1, api.ErrPrevTermMismatch, []string{"a"}, []string{"a"}},
		{"prev too large", "[1/1/a]", 1, 1, 2, api.ErrPrevIndexRange, []string{"a"}, []string{"a", "b"}},
		{"replace all", "[1/1/a 1/2/b]", 2, 1, 0, nil, []string{"x", "y"}, []string{"a", "b"}},
	}
	for _, tc := range cases {
		sv := api.New()
		mk(sv[1], tc.fTerm, tc.fCmds...)
		mk(sv[0], tc.lTerm, tc.lCmds...)
		err := api.Replicate(sv[0], sv[1], tc.prev)
		if got := fmtLog(api.Log(sv[1])); !errors.Is(err, tc.wantErr) || got != tc.want {
			t.Errorf("%s: err=%v follower=%s want %s", tc.name, err, got, tc.want)
		}
	}
}
func TestFaultInjection(t *testing.T) {
	sv := api.New()
	mk(sv[0], 1, "a")
	mk(sv[1], 2, "x")
	b0, b1 := api.Log(sv[0]), api.Log(sv[1])
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { return api.Append(sv[0], "") }, api.ErrEmptyCmd},
		{func() error { return api.Replicate(sv[0], sv[1], -1) }, api.ErrPrevIndexRange},
		{func() error { return api.Replicate(sv[0], sv[1], 5) }, api.ErrPrevIndexRange},
		{func() error { return api.Replicate(sv[0], sv[1], 1) }, api.ErrPrevTermMismatch},
	}
	for _, tc := range cases {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Errorf("err=%v want %v", err, tc.want)
		}
	}
	if api.ErrEmptyCmd == api.ErrPrevIndexRange || api.ErrPrevIndexRange == api.ErrPrevTermMismatch || api.ErrEmptyCmd == api.ErrPrevTermMismatch {
		t.Error("sentinel errors not distinct")
	}
	if !slices.Equal(api.Log(sv[0]), b0) || !slices.Equal(api.Log(sv[1]), b1) || api.Replicate(sv[0], sv[1], 0) != nil {
		t.Error("rejected op changed state or cluster unusable")
	}
}
func TestIncrementalCommit(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		sv := api.New()
		sv[0].SetTerm(1)
		for i := 0; i < m; i++ {
			api.Append(sv[0], "x")
		}
		api.Replicate(sv[0], sv[1], 0)
		ci1 := api.CommitIndex(sv[0])
		api.Append(sv[0], "y")
		api.Replicate(sv[0], sv[1], m)
		ci2 := api.CommitIndex(sv[0])
		if ci1 != m || ci2 != m+1 || checkedOf(sv[0]) != 1 {
			t.Errorf("m=%d ci1=%d ci2=%d checked=%d, want ci1=%d ci2=%d checked=1", m, ci1, ci2, checkedOf(sv[0]), m, m+1)
		}
	}
}
func TestConcurrentRead(t *testing.T) {
	sv := api.New()
	sv[0].SetTerm(1)
	for i := 0; i < 50; i++ {
		api.Append(sv[0], "x")
	}
	api.Replicate(sv[0], sv[1], 0)
	api.Replicate(sv[0], sv[2], 0)
	api.CommitIndex(sv[0])
	snaps, errs := make([][]entry.Entry, 16), make([]error, 16)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); <-start; snaps[g] = api.Log(sv[0]); errs[g] = api.SelfCheck() }(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < 16; g++ {
		if !slices.Equal(snaps[g], snaps[0]) || errs[g] != nil || errs[0] != nil {
			t.Errorf("goroutine %d: %v", g, errs[g])
		}
	}
}
