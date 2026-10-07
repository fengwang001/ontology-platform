package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// generator emits random but always-valid declaration sequences and applies
// them to both the engine and the naive reference implementation.
type generator struct {
	rng       *rand.Rand
	types     []string
	links     []string
	tags      []string
	roles     []string
	subjects  []string
	instances []string
}

func newGenerator(seed int64) *generator {
	return &generator{rng: rand.New(rand.NewSource(seed))}
}

func (g *generator) pick(xs []string) string {
	return xs[g.rng.Intn(len(xs))]
}

func (g *generator) maybePick(xs []string) (string, bool) {
	if len(xs) == 0 {
		return "", false
	}
	return g.pick(xs), true
}

// step applies one random mutation to both systems.
func (g *generator) step(e *Engine, n *naive) error {
	kind := g.rng.Intn(16)
	switch kind {
	case 0: // declare object type
		id := fmt.Sprintf("T%d", len(g.types))
		g.types = append(g.types, id)
		n.objectTypes[id] = true
		return e.DeclareObjectType(id)
	case 1: // declare link type between random declared types
		if len(g.types) == 0 {
			return nil
		}
		id := fmt.Sprintf("L%d", len(g.links))
		link := LinkType{ID: id, From: g.pick(g.types), To: g.pick(g.types)}
		g.links = append(g.links, id)
		n.links[id] = link
		return e.DeclareLinkType(id, link.From, link.To)
	case 2: // declare tag
		id := fmt.Sprintf("G%d", len(g.tags))
		g.tags = append(g.tags, id)
		n.tags[id] = true
		return e.DeclareTag(id)
	case 3: // attach tag
		tag, ok1 := g.maybePick(g.tags)
		ot, ok2 := g.maybePick(g.types)
		if !ok1 || !ok2 {
			return nil
		}
		if n.attach[ot] == nil {
			n.attach[ot] = map[string]bool{}
		}
		n.attach[ot][tag] = true
		return e.AttachTag(tag, ot)
	case 4: // detach tag
		tag, ok1 := g.maybePick(g.tags)
		ot, ok2 := g.maybePick(g.types)
		if !ok1 || !ok2 {
			return nil
		}
		delete(n.attach[ot], tag)
		return e.DetachTag(tag, ot)
	case 5: // declare propagation
		tag, ok1 := g.maybePick(g.tags)
		link, ok2 := g.maybePick(g.links)
		if !ok1 || !ok2 {
			return nil
		}
		dir := Direction(g.rng.Intn(2))
		k := propKey{tag: tag, link: link}
		if n.props[k] == nil {
			n.props[k] = map[Direction]bool{}
		}
		n.props[k][dir] = true
		return e.DeclarePropagation(tag, link, dir)
	case 6: // revoke propagation
		tag, ok1 := g.maybePick(g.tags)
		link, ok2 := g.maybePick(g.links)
		if !ok1 || !ok2 {
			return nil
		}
		k := propKey{tag: tag, link: link}
		if _, ok := n.props[k]; !ok {
			return nil
		}
		delete(n.props, k)
		return e.RevokePropagation(tag, link)
	case 7: // declare or remove block
		tag, ok1 := g.maybePick(g.tags)
		link, ok2 := g.maybePick(g.links)
		if !ok1 || !ok2 {
			return nil
		}
		k := blockKey{tag: tag, link: link}
		if g.rng.Intn(2) == 0 {
			n.blocks[k] = true
			return e.DeclareBlock(tag, link)
		}
		delete(n.blocks, k)
		return e.RemoveBlock(tag, link)
	case 8: // declare role
		id := fmt.Sprintf("R%d", len(g.roles))
		g.roles = append(g.roles, id)
		n.roleParents[id] = map[string]bool{}
		return e.DeclareRole(id)
	case 9: // add role parent (may close a cycle)
		if len(g.roles) == 0 {
			return nil
		}
		role, parent := g.pick(g.roles), g.pick(g.roles)
		if n.roleParents[role][parent] {
			return nil
		}
		n.roleParents[role][parent] = true
		return e.AddRoleParent(role, parent)
	case 10: // remove role parent
		if len(g.roles) == 0 {
			return nil
		}
		role, parent := g.pick(g.roles), g.pick(g.roles)
		if !n.roleParents[role][parent] {
			return nil
		}
		delete(n.roleParents[role], parent)
		return e.RemoveRoleParent(role, parent)
	case 11: // declare subject with a random role subset
		id := fmt.Sprintf("S%d", len(g.subjects))
		g.subjects = append(g.subjects, id)
		n.subjectRoles[id] = map[string]bool{}
		var roles []string
		for _, r := range g.roles {
			if g.rng.Intn(2) == 0 {
				roles = append(roles, r)
				n.subjectRoles[id][r] = true
			}
		}
		return e.DeclareSubject(id, roles...)
	case 12: // declare instance
		ot, ok := g.maybePick(g.types)
		if !ok {
			return nil
		}
		id := fmt.Sprintf("I%d", len(g.instances))
		g.instances = append(g.instances, id)
		n.instances[id] = ot
		return e.DeclareInstance(id, ot)
	case 13, 14: // set grant (allow or deny)
		role, ok1 := g.maybePick(g.roles)
		tag, ok2 := g.maybePick(g.tags)
		if !ok1 || !ok2 {
			return nil
		}
		effect := Allow
		if g.rng.Intn(2) == 0 {
			effect = Deny
		}
		n.grants[grantKey{role: role, tag: tag}] = effect
		return e.SetGrant(role, tag, effect)
	case 15: // unset grant
		role, ok1 := g.maybePick(g.roles)
		tag, ok2 := g.maybePick(g.tags)
		if !ok1 || !ok2 {
			return nil
		}
		k := grantKey{role: role, tag: tag}
		if _, ok := n.grants[k]; !ok {
			return nil
		}
		delete(n.grants, k)
		return e.UnsetGrant(role, tag)
	}
	return nil
}

func outcomeOfEngine(dec Decision, err error) naiveOutcome {
	if class := ClassOf(err); class != 0 {
		return naiveOutcome{class: class}
	}
	return naiveOutcome{allowed: dec.Allowed, reason: dec.Reason}
}

func compareSystems(t *testing.T, e *Engine, n *naive, g *generator) {
	t.Helper()
	// Compare effective instance tags.
	for _, inst := range g.instances {
		got, err := e.InstanceTags(inst)
		if err != nil {
			t.Fatalf("InstanceTags(%s): %v", inst, err)
		}
		wantMap := n.tagsOf(n.instances[inst])
		var want []string
		for tag := range wantMap {
			want = append(want, tag)
		}
		sort.Strings(want)
		if got == nil {
			got = []string{}
		}
		if want == nil {
			want = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("instance %s tags: engine=%v naive=%v", inst, got, want)
		}
	}
	// Compare access decisions on random triples (plus unknown IDs to
	// exercise the not-found class).
	subjects := append(append([]string{}, g.subjects...), "ghost-subject")
	instances := append(append([]string{}, g.instances...), "ghost-instance")
	tags := append(append([]string{}, g.tags...), "ghost-tag")
	for i := 0; i < 4; i++ {
		subject := g.pick(subjects)
		instance := g.pick(instances)
		tag := g.pick(tags)

		dec, err := e.Authorize(subject, instance, "read")
		got := outcomeOfEngine(dec, err)
		want := n.authorize(subject, instance)
		if got != want {
			t.Fatalf("Authorize(%s,%s): engine=%+v naive=%+v", subject, instance, got, want)
		}

		dec, err = e.CheckTag(subject, instance, tag)
		got = outcomeOfEngine(dec, err)
		want = n.checkTag(subject, instance, tag)
		if got != want {
			t.Fatalf("CheckTag(%s,%s,%s): engine=%+v naive=%+v", subject, instance, tag, got, want)
		}
	}
}

func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			e := NewEngine()
			n := newNaive()
			g := newGenerator(seed)
			for step := 0; step < 150; step++ {
				if err := g.step(e, n); err != nil {
					t.Fatalf("step %d: unexpected engine error: %v", step, err)
				}
				if step%5 == 4 {
					compareSystems(t, e, n, g)
				}
			}
			compareSystems(t, e, n, g)
		})
	}
}
