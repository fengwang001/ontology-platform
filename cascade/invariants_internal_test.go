package cascade

import "testing"

// checkInvariants 从前向属主切片全量重建派生结构并比对，仅供测试。
func (c *Controller) checkInvariants(t *testing.T, ctx string) {
	t.Helper()

	rebuiltDeps := map[string]map[string]struct{}{}
	blocking := map[string]int{}
	for id, o := range c.objects {
		for _, r := range o.owners {
			if _, ok := c.objects[r.OwnerID]; !ok {
				t.Fatalf("[%s] %s holds stale owner ref to removed %s", ctx, id, r.OwnerID)
			}
			if rebuiltDeps[r.OwnerID] == nil {
				rebuiltDeps[r.OwnerID] = map[string]struct{}{}
			}
			rebuiltDeps[r.OwnerID][id] = struct{}{}
			if r.Blocking {
				blocking[r.OwnerID]++
			}
		}
	}
	for owner, deps := range rebuiltDeps {
		got := c.dependents[owner]
		if len(got) != len(deps) {
			t.Fatalf("[%s] dependents[%s] size got %d want %d", ctx, owner, len(got), len(deps))
		}
		for d := range deps {
			if _, ok := got[d]; !ok {
				t.Fatalf("[%s] dependents[%s] missing %s", ctx, owner, d)
			}
		}
	}
	for owner, got := range c.dependents {
		if _, want := rebuiltDeps[owner]; !want && len(got) == 0 {
			continue
		}
		if _, want := rebuiltDeps[owner]; !want {
			t.Fatalf("[%s] stale dependents entry for %s", ctx, owner)
		}
	}
	for id, o := range c.objects {
		if got, want := blocking[id], o.blockingAliveDeps; got != want {
			t.Fatalf("[%s] %s blockingAliveDeps got %d want %d", ctx, id, want, got)
		}
		alive, fg := 0, 0
		deletingN := 0
		for _, r := range o.owners {
			owner := c.objects[r.OwnerID]
			alive++
			if owner.deleting {
				deletingN++
			}
			if owner.deleting && owner.strategy == Foreground {
				fg++
			}
		}
		if o.aliveOwners != alive {
			t.Fatalf("[%s] %s aliveOwners got %d want %d", ctx, id, o.aliveOwners, alive)
		}
		if o.fgOwners != fg {
			t.Fatalf("[%s] %s fgOwners got %d want %d", ctx, id, o.fgOwners, fg)
		}
		if o.deletingOwners != deletingN {
			t.Fatalf("[%s] %s deletingOwners got %d want %d", ctx, id, o.deletingOwners, deletingN)
		}
	}
}

func TestInvariantsSmoke(t *testing.T) {
	c := New()
	c.Create("a", nil, nil)
	c.Create("b", []OwnerRef{{OwnerID: "a", Blocking: true}}, []string{"f"})
	c.checkInvariants(t, "create")
	c.Delete("a", Foreground)
	c.checkInvariants(t, "fg-delete")
	c.RemoveFinalizer("b", "f")
	c.checkInvariants(t, "after-rmf")
}
