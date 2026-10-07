package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type naiveGraph struct {
	objects map[string]struct{}
	links   []Link
	configs map[string]LinkTypeConfig
}

type naiveResult struct {
	deleted   map[string]struct{}
	explicit  map[string]struct{}
	removed   map[Link]struct{}
	nullified map[Link]struct{}
	err       error
}

func newNaiveGraph(objects map[string]struct{}, links []Link, configs map[string]LinkTypeConfig) *naiveGraph {
	copiedObjects := make(map[string]struct{}, len(objects))
	for id := range objects {
		copiedObjects[id] = struct{}{}
	}
	return &naiveGraph{
		objects: copiedObjects,
		links:   append([]Link(nil), links...),
		configs: cloneConfigs(configs),
	}
}

func (g *naiveGraph) delete(root string) naiveResult {
	result := naiveResult{
		deleted:   map[string]struct{}{root: {}},
		explicit:  map[string]struct{}{root: {}},
		removed:   make(map[Link]struct{}),
		nullified: make(map[Link]struct{}),
	}
	if _, ok := g.objects[root]; !ok {
		result.err = ErrObjectNotFound
		return result
	}

	undefined := make(map[string]struct{})
	changed := true
	for changed {
		changed = false

		for _, objectID := range sortedIDs(result.deleted) {
			for _, link := range append([]Link(nil), g.links...) {
				touches := link.Source == objectID || link.Target == objectID
				if !touches {
					continue
				}
				_, isExplicit := result.explicit[objectID]
				if !isExplicit && link.Source != objectID {
					continue
				}
				if result.isDone(link) {
					continue
				}
				config, ok := g.configs[link.Type]
				if !ok {
					undefined[link.Type] = struct{}{}
					continue
				}
				peer := link.Target
				if link.Target == objectID {
					peer = link.Source
				}
				switch config.OnDelete {
				case Cascade:
					result.removed[link] = struct{}{}
					if _, exists := result.deleted[peer]; !exists {
						result.deleted[peer] = struct{}{}
						changed = true
					}
					result.explicit[peer] = struct{}{}
				case SetNull:
					result.nullified[link] = struct{}{}
					result.removed[link] = struct{}{}
				case Restrict:
				}
			}
		}

		for _, objectID := range sortedIDs(g.objects) {
			if _, deleting := result.deleted[objectID]; deleting {
				continue
			}
			if !g.hasPreserveType(objectID) {
				continue
			}
			preserved := false
			for _, link := range g.links {
				config, ok := g.configs[link.Type]
				if link.Target != objectID || !ok || !config.PreserveOnExists {
					continue
				}
				if _, sourceDeleted := result.deleted[link.Source]; sourceDeleted {
					continue
				}
				if _, null := result.nullified[link]; null {
					continue
				}
				preserved = true
			}
			if !preserved {
				result.deleted[objectID] = struct{}{}
				delete(result.explicit, objectID)
				changed = true
			}
		}
	}

	for _, objectID := range sortedIDs(result.deleted) {
		for _, link := range g.links {
			if link.Source != objectID && link.Target != objectID {
				continue
			}
			config := g.configs[link.Type]
			if config.OnDelete != Restrict {
				continue
			}
			peer := link.Target
			if link.Target == objectID {
				peer = link.Source
			}
			if _, peerDeleted := result.deleted[peer]; peerDeleted {
				continue
			}
			for _, other := range g.links {
				if other == link || other.Type != link.Type || other.Target != peer {
					continue
				}
				if _, sourceDeleted := result.deleted[other.Source]; sourceDeleted {
					continue
				}
				result.err = ErrDeleteRestricted
				return result
			}
		}
	}

	if len(undefined) > 0 {
		result.err = fmt.Errorf("%w: %q", ErrUndefinedLinkType, sortedIDs(undefined)[0])
		return result
	}

	for objectID := range result.deleted {
		for _, link := range g.links {
			if link.Source == objectID || link.Target == objectID {
				if _, null := result.nullified[link]; !null {
					result.removed[link] = struct{}{}
				}
			}
		}
	}
	for link := range result.nullified {
		delete(result.removed, link)
	}
	return result
}

func (r naiveResult) isDone(link Link) bool {
	_, removed := r.removed[link]
	_, nullified := r.nullified[link]
	return removed || nullified
}

func (g *naiveGraph) hasPreserveType(target string) bool {
	for _, link := range g.links {
		if link.Target == target && g.configs[link.Type].PreserveOnExists {
			return true
		}
	}
	return false
}

func cloneConfigs(configs map[string]LinkTypeConfig) map[string]LinkTypeConfig {
	result := make(map[string]LinkTypeConfig, len(configs))
	for name, config := range configs {
		result[name] = config
	}
	return result
}

func TestRandomGraphsMatchNaiveSimulation(t *testing.T) {
	actions := []DeleteAction{Cascade, SetNull, Restrict}
	for seed := int64(1); seed <= 300; seed++ {
		random := rand.New(rand.NewSource(seed))
		store := NewStore()
		configs := make(map[string]LinkTypeConfig)
		for typeIndex := 0; typeIndex < 5; typeIndex++ {
			name := fmt.Sprintf("t%d", typeIndex)
			config := LinkTypeConfig{Name: name, OnDelete: actions[typeIndex%3], PreserveOnExists: random.Intn(2) == 0}
			mustRegister(t, store, name, config.OnDelete, config.PreserveOnExists)
			configs[name] = config
		}

		objectCount := 1 + random.Intn(10)
		ids := make([]string, objectCount)
		for i := range ids {
			ids[i] = fmt.Sprintf("o%d", i)
			mustCreate(t, store, ids[i])
		}

		var links []Link
		linkCount := 5 + random.Intn(25)
		for i := 0; i < linkCount; i++ {
			link := Link{Type: fmt.Sprintf("t%d", random.Intn(5)), Source: ids[random.Intn(objectCount)], Target: ids[random.Intn(objectCount)]}
			if err := store.AddLink(link); err != nil {
				t.Fatal(seed, err)
			}
			links = append(links, link)
		}

		objects, committedLinks, snapshotConfigs := store.Snapshot()
		naive := newNaiveGraph(objects, committedLinks, snapshotConfigs)
		root := ids[random.Intn(objectCount)]
		got, err := store.DeleteObject(root)
		want := naive.delete(root)

		if !errors.Is(err, want.err) {
			t.Fatalf("seed=%d root=%s err=%v want=%v links=%v configs=%v", seed, root, err, want.err, links, configs)
		}
		if err != nil {
			objectsAfter, linksAfter, _ := store.Snapshot()
			if len(objectsAfter) != objectCount || len(linksAfter) != len(committedLinks) {
				t.Fatalf("seed=%d failed delete mutated committed state", seed)
			}
			continue
		}

		gotDeleted := stringSliceSet(got.DeletedObjects)
		if !equalIDSets(gotDeleted, want.deleted) {
			t.Fatalf("seed=%d root=%s deleted=%v want=%v links=%v configs=%v", seed, root, got.DeletedObjects, sortedIDs(want.deleted), links, configs)
		}
		if !equalLinkSets(linkSliceSet(got.RemovedLinks), want.removed) || !equalLinkSets(linkSliceSet(got.NullifiedLinks), want.nullified) {
			t.Fatalf("seed=%d root=%s links=%v/%v want=%v/%v", seed, root, got.RemovedLinks, got.NullifiedLinks, want.removed, want.nullified)
		}
	}
}

func stringSliceSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func linkSliceSet(values []Link) map[Link]struct{} {
	result := make(map[Link]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func equalIDSets(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, ok := right[value]; !ok {
			return false
		}
	}
	return true
}

func equalLinkSets(left, right map[Link]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, ok := right[value]; !ok {
			return false
		}
	}
	return true
}
