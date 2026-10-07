package ontology

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// TestConcurrentChecksDoNotCorruptState hammers one engine with concurrent
// reads while another goroutine applies mutations; a twin engine receives
// the same mutations without read pressure. Final derived state must be
// identical, proving reads never interfere with mutations.
func TestConcurrentChecksDoNotCorruptState(t *testing.T) {
	e := NewEngine()
	twin := NewEngine()
	g := newGenerator(42)
	n := newNaive()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				dec, err := e.Authorize(fmt.Sprintf("S%d", id%3), fmt.Sprintf("I%d", id%5), "read")
				if err == nil && dec.Allowed {
					for _, td := range dec.Tags {
						if !td.Allowed {
							t.Errorf("torn decision: allowed with denied tag %+v", td)
						}
					}
				}
				_, _ = e.CheckTag(fmt.Sprintf("S%d", id%3), fmt.Sprintf("I%d", id%5), fmt.Sprintf("G%d", id%4))
			}
		}(w)
	}

	for i := 0; i < 400; i++ {
		if err := g.step(e, n); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	g2 := newGenerator(42)
	n2 := newNaive()
	for i := 0; i < 400; i++ {
		if err := g2.step(twin, n2); err != nil {
			t.Fatalf("twin step %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	if !reflect.DeepEqual(e.typeTags, twin.typeTags) ||
		!reflect.DeepEqual(e.typeBlocked, twin.typeBlocked) ||
		!reflect.DeepEqual(e.roleGrants, twin.roleGrants) ||
		!reflect.DeepEqual(e.roleCyclic, twin.roleCyclic) {
		t.Fatal("concurrent reads corrupted derived state")
	}
}

// TestRevocationIsAtomic removes a link type whose cascaded recompute
// strips two inherited tags from one instance type in a single mutation.
// The mutation loop toggles between states whose serial tag sets are
// {}, {t1} and {t1,t2}; observing exactly {t2} is impossible in any serial
// execution and would prove a torn read inside one mutation's recompute.
func TestRevocationIsAtomic(t *testing.T) {
	e := NewEngine()
	must(t, e.DeclareObjectType("Folder"))
	must(t, e.DeclareObjectType("Doc"))
	must(t, e.DeclareLinkType("contains", "Folder", "Doc"))
	must(t, e.DeclareTag("t1"))
	must(t, e.DeclareTag("t2"))
	must(t, e.AttachTag("t1", "Folder"))
	must(t, e.AttachTag("t2", "Folder"))
	must(t, e.DeclarePropagation("t1", "contains", Downstream))
	must(t, e.DeclarePropagation("t2", "contains", Downstream))
	must(t, e.DeclareInstance("i", "Doc"))
	must(t, e.DeclareRole("r"))
	must(t, e.DeclareSubject("s", "r"))
	must(t, e.SetGrant("r", "t1", Allow))
	must(t, e.SetGrant("r", "t2", Allow))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				dec, err := e.Authorize("s", "i", "read")
				if err != nil {
					continue
				}
				if len(dec.Tags) == 1 && dec.Tags[0].Tag == "t2" {
					t.Errorf("observed torn revocation state: %+v", dec.Tags)
				}
				tags, err := e.InstanceTags("i")
				if err == nil && len(tags) == 1 && tags[0] == "t2" {
					t.Errorf("observed torn inheritance state: %v", tags)
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		// One mutation cascades: both propagations die together.
		must(t, e.RemoveLinkType("contains"))
		must(t, e.DeclareLinkType("contains", "Folder", "Doc"))
		must(t, e.DeclarePropagation("t1", "contains", Downstream))
		must(t, e.DeclarePropagation("t2", "contains", Downstream))
	}
	close(stop)
	wg.Wait()
}

// TestIncrementalEqualsRebuild proves that the state reached by incremental
// mutations is identical to recomputing from the current declarations.
func TestIncrementalEqualsRebuild(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		e := NewEngine()
		n := newNaive()
		g := newGenerator(1000 + seed)
		for i := 0; i < 200; i++ {
			must(t, g.step(e, n))
		}
		fresh := &Engine{
			objectTypes:  copySet(e.objectTypes),
			linkTypes:    copyLinks(e.linkTypes),
			tags:         copySet(e.tags),
			roleParents:  copySetMap(e.roleParents),
			subjectRoles: copySetMap(e.subjectRoles),
			instances:    copyStrMap(e.instances),
			attachments:  copySetMap(e.attachments),
			propagations: copyProps(e.propagations),
			blocks:       copyBlocks(e.blocks),
			grants:       copyGrants(e.grants),
			audit:        NopLogger{},
		}
		fresh.recomputeLocked()
		if !reflect.DeepEqual(e.typeTags, fresh.typeTags) ||
			!reflect.DeepEqual(e.typeBlocked, fresh.typeBlocked) ||
			!reflect.DeepEqual(e.roleGrants, fresh.roleGrants) ||
			!reflect.DeepEqual(e.roleCyclic, fresh.roleCyclic) {
			t.Fatalf("seed %d: incremental state diverged from rebuild", seed)
		}
	}
}

func copySet(in map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func copyStrMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyLinks(in map[string]LinkType) map[string]LinkType {
	out := map[string]LinkType{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copySetMap(in map[string]map[string]struct{}) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for k, v := range in {
		out[k] = copySet(v)
	}
	return out
}

func copyProps(in map[propKey]map[Direction]bool) map[propKey]map[Direction]bool {
	out := map[propKey]map[Direction]bool{}
	for k, v := range in {
		dirs := map[Direction]bool{}
		for d, b := range v {
			dirs[d] = b
		}
		out[k] = dirs
	}
	return out
}

func copyBlocks(in map[blockKey]struct{}) map[blockKey]struct{} {
	out := map[blockKey]struct{}{}
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func copyGrants(in map[grantKey]Effect) map[grantKey]Effect {
	out := map[grantKey]Effect{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
