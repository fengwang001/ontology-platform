package ontology

import (
	"sync"
	"testing"
)

// Locate 与 Add/Remove 并发执行：不得 panic、不得返回空串，
// 且每个返回值都必须是并发期间真实存在过的节点。
func TestConcurrentLocateWithMutation(t *testing.T) {
	r := New()
	const baseNodes = 5
	for i := 0; i < baseNodes; i++ {
		if err := r.Add(nodeID(i), 50); err != nil {
			t.Fatal(err)
		}
	}

	known := make(map[string]bool)
	for i := 0; i < baseNodes+10; i++ {
		known[nodeID(i)] = true
	}

	keys := GenerateKeys(2000)
	const readers = 8
	results := make([][]string, readers)
	for i := range results {
		results[i] = make([]string, len(keys))
	}

	var wg sync.WaitGroup
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, k := range keys {
				owner, err := r.Locate(k)
				if err != nil {
					t.Errorf("Locate(%q): %v", k, err)
					return
				}
				results[g][i] = owner
			}
		}(g)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := baseNodes; i < baseNodes+10; i++ {
			if err := r.Add(nodeID(i), 50); err != nil {
				t.Errorf("Add: %v", err)
				return
			}
		}
		for i := baseNodes; i < baseNodes+10; i++ {
			if err := r.Remove(nodeID(i)); err != nil {
				t.Errorf("Remove: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	for g := 0; g < readers; g++ {
		for i, owner := range results[g] {
			if owner == "" {
				t.Fatalf("reader %d key %q got empty owner", g, keys[i])
			}
			if !known[owner] {
				t.Fatalf("reader %d key %q got unknown owner %q", g, keys[i], owner)
			}
		}
	}
}
