package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type naiveCommit struct {
	id      string
	parents []*naiveCommit
	files   map[string]string
}

type naiveState struct {
	introduced Attribution
	parent     *naiveState
}

func naiveMatch(child, parent []string) []int {
	return matchLines(child, parent)
}

func naiveStates(cache map[string][]*naiveState, cm *naiveCommit, path string) []*naiveState {
	key := cm.id + "\x00" + path
	if states, ok := cache[key]; ok {
		return states
	}
	child := splitLines(cm.files[path])
	states := make([]*naiveState, len(child))
	matched := make([]bool, len(child))
	for _, parent := range cm.parents {
		if _, ok := parent.files[path]; !ok {
			continue
		}
		parentStates := naiveStates(cache, parent, path)
		matches := naiveMatch(child, splitLines(parent.files[path]))
		for i, j := range matches {
			if !matched[i] && j >= 0 {
				states[i] = &naiveState{parent: parentStates[j]}
				matched[i] = true
			}
		}
	}
	for i := range states {
		if states[i] == nil {
			states[i] = &naiveState{introduced: Attribution{CommitID: cm.id, Path: path, Line: i + 1}}
		}
	}
	cache[key] = states
	return states
}

func naiveAttribute(state *naiveState, ignored map[string]struct{}) Attribution {
	if state.parent != nil {
		return naiveAttribute(state.parent, ignored)
	}
	if _, ok := ignored[state.introduced.CommitID]; !ok {
		return state.introduced
	}
	result := state.introduced
	result.Ignored = true
	return result
}

func TestConcurrentLoadAndQuerySerialEquivalence(t *testing.T) {
	svc := NewService()
	loadCommit(t, svc, CommitInput{ID: "root", Files: map[string]string{"f": "0\n"}})

	const goroutines = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	var success int
	var duplicates int
	var mu sync.Mutex
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := svc.Load(CommitInput{
				ID:        "same",
				ParentIDs: []string{"root"},
				Files:     map[string]string{"f": "0\n1\n"},
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if errors.Is(err, ErrDuplicateCommit) {
				duplicates++
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 || duplicates != goroutines-1 {
		t.Fatalf("input=%d concurrent same-id loads actual=(success=%d duplicate=%d) criterion=one success", goroutines, success, duplicates)
	}
	t.Logf("input=%d concurrent same-id loads actual=(success=%d duplicate=%d) criterion=serializable", goroutines, success, duplicates)

	var queryWG sync.WaitGroup
	results := make([][]Attribution, goroutines)
	start = make(chan struct{})
	for i := 0; i < goroutines; i++ {
		queryWG.Add(1)
		go func(index int) {
			defer queryWG.Done()
			<-start
			got, err := svc.Blame("same", "f", 0)
			if err != nil {
				t.Errorf("concurrent query failed: %v", err)
				return
			}
			results[index] = got
		}(i)
	}
	close(start)
	queryWG.Wait()
	want := []Attribution{attr("root", "f", 1, false), attr("same", "f", 2, false)}
	for i, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("input=concurrent query %d actual=%#v criterion=%#v", i, got, want)
		}
	}
	t.Logf("input=%d concurrent queries actual=identical rows criterion=no half-loaded commit", goroutines)
}

func TestPerformanceIndependentPathsAndRepeatedQuery(t *testing.T) {
	svc := NewService()
	loadCommit(t, svc, CommitInput{ID: "root", Files: map[string]string{"f": "root\n", "z": "rootz\n"}})
	for i := 0; i < 100; i++ {
		parent := "root"
		if i > 0 {
			parent = fmt.Sprintf("u%03d", i-1)
		}
		loadCommit(t, svc, CommitInput{
			ID:        fmt.Sprintf("u%03d", i),
			ParentIDs: []string{parent},
			Files: map[string]string{
				"z": fmt.Sprintf("rootz\nunrelated%d\n", i),
			},
		})
	}
	loadCommit(t, svc, CommitInput{
		ID:        "target",
		ParentIDs: []string{"root"},
		Files:     map[string]string{"f": "root\ntarget\n"},
	})

	svc.lineage.stats.stateBuilds = 0
	svc.lineage.stats.parentChecks = 0
	got, err := svc.Blame("target", "f", 0)
	if err != nil {
		t.Fatal(err)
	}
	if svc.lineage.stats.stateBuilds != 2 || svc.lineage.stats.parentChecks != 1 {
		t.Fatalf("input=100 unrelated commits actual=(builds=%d checks=%d) criterion=2 states and 1 parent check", svc.lineage.stats.stateBuilds, svc.lineage.stats.parentChecks)
	}
	t.Logf("input=100 unrelated commits actual=(builds=%d checks=%d) criterion=cold query ignores unrelated history", svc.lineage.stats.stateBuilds, svc.lineage.stats.parentChecks)

	svc.lineage.stats.stateBuilds = 0
	svc.lineage.stats.parentChecks = 0
	repeat, err := svc.Blame("target", "f", 0)
	if err != nil || !reflect.DeepEqual(repeat, got) {
		t.Fatalf("actual=(%#v,%v) criterion=stable cached result", repeat, err)
	}
	if svc.lineage.stats.stateBuilds != 0 || svc.lineage.stats.parentChecks != 0 {
		t.Fatalf("input=repeated query actual=(builds=%d checks=%d) criterion=no history traversal", svc.lineage.stats.stateBuilds, svc.lineage.stats.parentChecks)
	}
	t.Logf("input=repeated query actual=(builds=%d checks=%d) criterion=constant repeated-query work", svc.lineage.stats.stateBuilds, svc.lineage.stats.parentChecks)

	rejectedBefore := svc.rejected
	_, _ = svc.Blame("missing", "f", 0)
	if svc.rejected != rejectedBefore+1 {
		t.Fatal("rejected query did not record rejection")
	}
	if svc.lineage.stats.stateBuilds != 0 || svc.lineage.stats.parentChecks != 0 {
		t.Fatal("rejected query touched resolution work")
	}
	t.Log("input=invalid query actual=resolution counters unchanged criterion=no cache or statistics side effect")
}

func TestRandomGraphMatchesNaiveModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 99, 123456} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			svc := NewService()
			var ordered []*naiveCommit
			byID := make(map[string]*naiveCommit)

			loadNaive := func(input CommitInput, cm *naiveCommit) {
				t.Helper()
				if err := svc.Load(input); err != nil {
					t.Fatalf("input=%#v actual=%v criterion=generated graph loads", input, err)
				}
				ordered = append(ordered, cm)
				byID[cm.id] = cm
			}

			root := &naiveCommit{id: "c000", files: map[string]string{
				"a": randomContent(rng, 4, "a"),
				"b": randomContent(rng, 4, "b"),
			}}
			loadNaive(CommitInput{ID: root.id, Files: cloneNaiveFiles(root.files)}, root)

			const count = 40
			for i := 1; i <= count; i++ {
				id := fmt.Sprintf("c%03d", i)
				parents := randomParents(rng, ordered)
				files := randomEvolvedFiles(rng, parents)
				cm := &naiveCommit{id: id, files: files}
				for _, parent := range parents {
					cm.parents = append(cm.parents, byID[parent])
				}
				input := CommitInput{ID: id, Files: cloneNaiveFiles(files)}
				for _, parent := range parents {
					input.ParentIDs = append(input.ParentIDs, parent)
				}
				loadNaive(input, cm)
			}

			ignoreSets := []map[string]struct{}{
				{},
				{ordered[1].id: {}},
				{ordered[2].id: {}, ordered[3].id: {}},
				{ordered[len(ordered)-2].id: {}, ordered[len(ordered)-1].id: {}},
			}
			var versions []int
			for i := 1; i < len(ignoreSets); i++ {
				ids := make([]string, 0, len(ignoreSets[i]))
				for id := range ignoreSets[i] {
					ids = append(ids, id)
				}
				version, err := svc.AddIgnoreList(ids)
				if err != nil {
					t.Fatalf("seed=%d list=%v err=%v", seed, ids, err)
				}
				versions = append(versions, version)
			}
			if !reflect.DeepEqual(versions, []int{1, 2, 3}) {
				t.Fatalf("actual versions=%v criterion=1..3", versions)
			}

			cache := make(map[string][]*naiveState)
			paths := []string{"a", "b"}
			for _, cm := range ordered {
				for _, path := range paths {
					if _, ok := cm.files[path]; !ok {
						continue
					}
					naiveRaw := naiveStates(cache, cm, path)
					for version, ignored := range ignoreSets {
						want := make([]Attribution, len(naiveRaw))
						for i, state := range naiveRaw {
							want[i] = naiveAttribute(state, ignored)
						}
						got, err := svc.Blame(cm.id, path, version)
						if err != nil {
							t.Fatalf("input=(%s,%s,%d) err=%v", cm.id, path, version, err)
						}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("seed=%d input=(%s,%s,%d) actual=%#v criterion=naive %#v", seed, cm.id, path, version, got, want)
						}
						t.Logf("seed=%d input=(%s,%s,%d) actual=%d rows criterion=naive row-by-row equality", seed, cm.id, path, version, len(got))
					}
				}
			}
		})
	}
}

func randomContent(rng *rand.Rand, lines int, prefix string) string {
	var builder strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&builder, "%s-%d-%d\n", prefix, i, rng.Intn(4))
	}
	return builder.String()
}

func randomParents(rng *rand.Rand, ordered []*naiveCommit) []string {
	parentCount := 1
	if len(ordered) >= 4 && rng.Intn(3) == 0 {
		parentCount = 2
	}
	seen := make(map[string]struct{})
	var parents []string
	for len(parents) < parentCount {
		id := ordered[rng.Intn(len(ordered))].id
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		parents = append(parents, id)
	}
	return parents
}

func randomEvolvedFiles(rng *rand.Rand, parentIDs []string) map[string]string {
	files := make(map[string]string)
	for _, path := range []string{"a", "b"} {
		if rng.Intn(5) == 0 {
			continue
		}
		files[path] = evolvedContent(rng, parentIDs, path)
	}
	return files
}

func evolvedContent(rng *rand.Rand, parentIDs []string, path string) string {
	_ = parentIDs
	lines := 2 + rng.Intn(5)
	return randomContent(rng, lines, path)
}

func cloneNaiveFiles(files map[string]string) map[string]string {
	clone := make(map[string]string, len(files))
	for path, content := range files {
		clone[path] = content
	}
	return clone
}
