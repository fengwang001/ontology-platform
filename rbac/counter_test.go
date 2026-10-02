package rbac

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// buildCounterScene creates roles lead/dev/noise, users m1 and m2 assigned
// lead, and a session k1 of m1 with lead activated; then it adds `noise`
// unrelated users, each with its own session activating the unrelated role.
func buildCounterScene(t *testing.T, noise int) *RBAC {
	t.Helper()
	r := newRBAC(t, 1000, 10)
	addRoles(t, r, "lead", "dev", "noise")
	mustOK(t, r.AddUser("m1"))
	mustOK(t, r.AddUser("m2"))
	mustOK(t, r.AssignUser("m1", "lead"))
	mustOK(t, r.AssignUser("m2", "lead"))
	mustOK(t, r.CreateSession("k1", "m1"))
	mustOK(t, r.Activate("k1", "lead"))
	for i := 0; i < noise; i++ {
		u := fmt.Sprintf("nu%05d", i)
		s := fmt.Sprintf("ns%05d", i)
		mustOK(t, r.AddUser(u))
		mustOK(t, r.AssignUser(u, "noise"))
		mustOK(t, r.CreateSession(s, u))
		mustOK(t, r.Activate(s, "noise"))
	}
	return r
}

// The unexported counters of AddInherit/DeleteInherit count only affected
// users/sessions; 10^4 unrelated users and sessions must not change them.
func TestCountersIgnoreUnrelatedUsersAndSessions(t *testing.T) {
	run := func(t *testing.T, noise int) (addCounts, delCounts [2]int, pairs []string) {
		r := buildCounterScene(t, noise)
		mustOK(t, r.AddInherit("lead", "dev"))
		u, s := r.LastCheckCounts()
		addCounts = [2]int{u, s}
		// Make k1 explicitly activate dev so DeleteInherit cascades into it.
		mustOK(t, r.Activate("k1", "dev"))
		ps, err := r.DeleteInherit("lead", "dev")
		mustOK(t, err)
		u, s = r.LastCheckCounts()
		delCounts = [2]int{u, s}
		pairs = pairsToStrings(ps)
		return
	}

	add0, del0, pairs0 := run(t, 0)
	add1, del1, pairs1 := run(t, 10000)

	if add0 != add1 || del0 != del1 {
		t.Fatalf("counts differ with noise: add %v vs %v, del %v vs %v", add0, add1, del0, del1)
	}
	if !reflect.DeepEqual(pairs0, pairs1) {
		t.Fatalf("cascade differs with noise: %v vs %v", pairs0, pairs1)
	}
	// Exact expected counts: 2 affected users, 1 affected session.
	if add0 != [2]int{2, 1} {
		t.Fatalf("AddInherit counts=%v, want [2 1]", add0)
	}
	if del0 != [2]int{2, 1} {
		t.Fatalf("DeleteInherit counts=%v, want [2 1]", del0)
	}
	if !reflect.DeepEqual(pairs0, []string{"k1/dev"}) {
		t.Fatalf("cascade=%v, want [k1/dev]", pairs0)
	}
}

// checkInvariants verifies the cached sets, the reverse indexes and all
// constraints against a from-scratch recomputation.
func checkInvariants(t *testing.T, r *RBAC) {
	t.Helper()
	for name, u := range r.users {
		want := r.computeAuth(u)
		if !reflect.DeepEqual(sortedOf(want), sortedOf(u.auth)) {
			t.Fatalf("user %s: cached Auth=%v, recomputed=%v", name, sortedOf(u.auth), sortedOf(want))
		}
	}
	for sid, s := range r.sessions {
		for role := range s.active {
			if !r.users[s.owner].auth[role] {
				t.Fatalf("session %s: explicit %s not in Auth(%s)", sid, role, s.owner)
			}
		}
		want := r.computeEff(s)
		if !reflect.DeepEqual(sortedOf(want), sortedOf(s.eff)) {
			t.Fatalf("session %s: cached Eff=%v, recomputed=%v", sid, sortedOf(s.eff), sortedOf(want))
		}
	}
	// Reverse indexes must match a full rebuild.
	authIdx := map[string]map[string]bool{}
	effIdx := map[string]map[string]bool{}
	explIdx := map[string]map[string]bool{}
	for name, u := range r.users {
		for role := range u.auth {
			idxAdd(authIdx, role, name)
		}
	}
	for sid, s := range r.sessions {
		for role := range s.eff {
			idxAdd(effIdx, role, sid)
		}
		for role := range s.active {
			idxAdd(explIdx, role, sid)
		}
	}
	for role := range r.roles {
		if got, want := sortedOf(r.authIdx[role]), sortedOf(authIdx[role]); !reflect.DeepEqual(got, want) {
			t.Fatalf("authIdx[%s]=%v, want %v", role, got, want)
		}
		if got, want := sortedOf(r.effIdx[role]), sortedOf(effIdx[role]); !reflect.DeepEqual(got, want) {
			t.Fatalf("effIdx[%s]=%v, want %v", role, got, want)
		}
		if got, want := sortedOf(r.explIdx[role]), sortedOf(explIdx[role]); !reflect.DeepEqual(got, want) {
			t.Fatalf("explIdx[%s]=%v, want %v", role, got, want)
		}
	}
	// All constraints must hold at all times.
	for _, c := range r.ssd {
		for name, u := range r.users {
			if intersectCount(u.auth, c.rs) >= c.n {
				t.Fatalf("SSD %s violated by user %s", c.name, name)
			}
		}
	}
	for _, c := range r.dsd {
		for sid, s := range r.sessions {
			if intersectCount(s.eff, c.rs) >= c.n {
				t.Fatalf("DSD %s violated by session %s", c.name, sid)
			}
		}
	}
}

// Concurrent calls must be race-free and leave the state consistent.
func TestConcurrentOperations(t *testing.T) {
	r := newRBAC(t, 1000, 1000)
	addRoles(t, r, "base", "x", "y")
	mustOK(t, r.AssignPerm("base", "p"))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			user := fmt.Sprintf("u%d", g)
			if err := r.AddUser(user); err != nil {
				t.Errorf("AddUser: %v", err)
				return
			}
			for i := 0; i < 100; i++ {
				sid := fmt.Sprintf("s%d-%d", g, i)
				_ = r.AssignUser(user, "base")
				_ = r.CreateSession(sid, user)
				_ = r.Activate(sid, "base")
				_ = r.Check(sid, "p")
				_ = r.AddInherit("x", "y")
				_ = r.Deactivate(sid, "base")
				_, _ = r.DeleteInherit("x", "y")
				_ = r.DeleteSession(sid)
				_, _ = r.DeassignUser(user, "base")
			}
		}(g)
	}
	wg.Wait()
	checkInvariants(t, r)
}

// The same operation sequence replayed twice must produce identical outputs.
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		r := newRBAC(t, 3, 4)
		var out []string
		record := func(err error) {
			if err == nil {
				out = append(out, "OK")
			} else {
				out = append(out, err.Error())
			}
		}
		for _, role := range []string{"r1", "r2", "r3"} {
			record(r.AddRole(role))
		}
		record(r.AddUser("u"))
		record(r.AddInherit("r1", "r2"))
		record(r.AddInherit("r2", "r3"))
		record(r.AddSSD("s", []string{"r2", "r3"}, 2))
		record(r.AssignUser("u", "r1"))
		record(r.AssignUser("u", "r3"))
		record(r.CreateSession("s1", "u"))
		record(r.Activate("s1", "r1"))
		record(r.Deactivate("s1", "r3"))
		ps, err := r.DeleteInherit("r1", "r2")
		record(err)
		out = append(out, fmt.Sprintf("%v", pairsToStrings(ps)))
		return out
	}
	first, second := run(), run()
	sort.Strings(first) // sort both identically; order preserved anyway
	sort.Strings(second)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\n%v\n%v", first, second)
	}
}
