package traversal

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestConcurrentPagesAndMutationsMatchSnapshots(t *testing.T) {
	graph := randomGraph(rand.New(rand.NewSource(42)), 40, 90)
	service := NewService(graph)
	request := PageRequest{
		StartObjectID: "o00",
		BatchSize:     7,
		Mode:          ExplicitTruncation,
		HopLimits:     []int{5, 4, 3},
		MaxHops:       4,
	}

	var servicePages []Page
	var wg sync.WaitGroup
	page, err := service.Page(request)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	servicePages = append(servicePages, page)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for page.NextCursor != "" {
			next, nextErr := service.Page(PageRequest{
				Cursor:    page.NextCursor,
				BatchSize: request.BatchSize,
				Mode:      request.Mode,
			})
			if nextErr != nil {
				t.Errorf("concurrent page: %v", nextErr)
				return
			}
			page = next
			servicePages = append(servicePages, page)
		}
	}()
	go func() {
		defer wg.Done()
		mutateConcurrently(graph)
	}()
	wg.Wait()

	initialSnapshot := serviceSnapshot(t, service, servicePages[0].NextCursor)
	expectedItems, expectedMarkers, err := FullTraversal(initialSnapshot, request.StartObjectID, request.Mode, request.HopLimits, request.MaxHops)
	if err != nil {
		t.Fatal(err)
	}
	expectedPages, err := ChunkFullTraversal(expectedItems, expectedMarkers, request.BatchSize)
	if err != nil {
		t.Fatal(err)
	}
	assertPagesEquivalent(t, servicePages, expectedPages)
}

func TestRandomizedPaginationMatchesNaiveSnapshotOracle(t *testing.T) {
	for seed := int64(1); seed <= 120; seed++ {
		random := rand.New(rand.NewSource(seed))
		objectCount := random.Intn(18) + 3
		linkCount := random.Intn(45) + 2
		graph := randomGraph(random, objectCount, linkCount)
		startID := fmt.Sprintf("o%02d", random.Intn(8))
		if !graph.Snapshot().HasObject(startID) {
			continue
		}
		mode := []Mode{SilentDrop, ExplicitTruncation}[random.Intn(2)]
		hopLimits := []int{random.Intn(8), random.Intn(8), random.Intn(8)}
		maxHops := random.Intn(4)
		batchSize := random.Intn(5) + 1
		snapshot := graph.Snapshot()
		service := NewService(graph)
		page, err := service.Page(PageRequest{
			StartObjectID: startID,
			BatchSize:     batchSize,
			Mode:          mode,
			HopLimits:     hopLimits,
			MaxHops:       maxHops,
		})
		if err != nil {
			t.Fatalf("seed %d: first page: %v", seed, err)
		}
		mutateSeedGraph(graph, seed)
		servicePages := []Page{page}
		for page.NextCursor != "" {
			page, err = service.Page(PageRequest{
				Cursor:    page.NextCursor,
				BatchSize: batchSize,
				Mode:      mode,
			})
			if err != nil {
				t.Fatalf("seed %d (objects=%d): next page: %v", seed, objectCount, err)
			}
			servicePages = append(servicePages, page)
		}

		expectedItems, expectedMarkers, err := FullTraversal(snapshot, startID, mode, hopLimits, maxHops)
		if err != nil {
			t.Fatalf("seed %d: oracle: %v", seed, err)
		}
		expectedPages, err := ChunkFullTraversal(expectedItems, expectedMarkers, batchSize)
		if err != nil {
			t.Fatalf("seed %d: chunk oracle: %v", seed, err)
		}
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			assertPagesEquivalent(t, servicePages, expectedPages)
		})
	}
}

func TestHistoryChecksAreConstantPerPage(t *testing.T) {
	graph := randomGraph(rand.New(rand.NewSource(77)), 30, 80)
	service := NewService(graph)
	page, err := service.Page(PageRequest{StartObjectID: "o00", BatchSize: 1, Mode: SilentDrop, MaxHops: 3})
	if err != nil {
		t.Fatal(err)
	}
	pages := 1
	for page.NextCursor != "" {
		page, err = service.Page(PageRequest{Cursor: page.NextCursor, BatchSize: 1, Mode: SilentDrop, MaxHops: 3})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		afterTotal, afterEach := service.HistoryChecks()
		if afterTotal != int64(pages-1) {
			t.Fatalf("total checks after %d pages = %d, want %d", pages, afterTotal, pages-1)
		}
		var traversalID string
		for id := range afterEach {
			traversalID = id
		}
		if afterEach[traversalID] != int64(pages-1) {
			t.Fatalf("per-traversal checks after %d pages = %d", pages, afterEach[traversalID])
		}
	}
	if pages < 5 {
		t.Fatalf("test graph returned only %d pages", pages)
	}
}

func randomGraph(random *rand.Rand, objectCount int, linkCount int) *Graph {
	graph := NewGraph()
	for i := 0; i < objectCount; i++ {
		graph.PutObject(Object{ID: fmt.Sprintf("o%02d", i)})
	}
	for i := 0; i < linkCount; i++ {
		from := random.Intn(objectCount)
		to := random.Intn(objectCount)
		graph.PutLink(Link{
			ID:     fmt.Sprintf("e%03d", i),
			FromID: fmt.Sprintf("o%02d", from),
			ToID:   fmt.Sprintf("o%02d", to),
		})
	}
	return graph
}

func serviceSnapshot(t *testing.T, service *Service, cursor string) Snapshot {
	t.Helper()
	if cursor == "" {
		return Snapshot{}
	}
	payload, err := decodeCursor(cursor, service.key)
	if err != nil {
		t.Fatalf("decode service cursor: %v", err)
	}
	state, ok := service.states[payload.TraversalID]
	if !ok {
		t.Fatal("service cursor has no state")
	}
	return state.snapshot
}

func mutateConcurrently(graph *Graph) {
	random := rand.New(rand.NewSource(99))
	for i := 0; i < 200; i++ {
		if random.Intn(2) == 0 {
			graph.PutObject(Object{ID: fmt.Sprintf("extra-%03d", random.Intn(80))})
		} else {
			from := random.Intn(40)
			to := random.Intn(40)
			graph.PutLink(Link{
				ID:     fmt.Sprintf("e-extra-%03d", i),
				FromID: fmt.Sprintf("o%02d", from),
				ToID:   fmt.Sprintf("o%02d", to),
			})
		}
	}
}

func mutateSeedGraph(graph *Graph, seed int64) {
	random := rand.New(rand.NewSource(seed + 10000))
	for i := 0; i < 30; i++ {
		graph.PutObject(Object{ID: fmt.Sprintf("mut-%02d-%02d", seed%100, random.Intn(20))})
	}
}

func assertPagesEquivalent(t *testing.T, got []Page, want []Page) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("pages len = %d, want %d; got items %v", len(got), len(want), itemIDs(got))
	}
	for i := range want {
		if len(got[i].Items) != len(want[i].Items) {
			t.Fatalf("page %d items = %v, want %v", i, itemIDs([]Page{got[i]}), itemIDs([]Page{want[i]}))
		}
		for j := range want[i].Items {
			if got[i].Items[j].Object.ID != want[i].Items[j].Object.ID || got[i].Items[j].Hop != want[i].Items[j].Hop {
				t.Fatalf("page %d item %d = %#v, want %#v", i, j, got[i].Items[j], want[i].Items[j])
			}
		}
		if fmt.Sprint(got[i].Truncations) != fmt.Sprint(want[i].Truncations) {
			t.Fatalf("page %d markers = %#v, want %#v", i, got[i].Truncations, want[i].Truncations)
		}
		if got[i].Complete != want[i].Complete {
			t.Fatalf("page %d complete = %v, want %v", i, got[i].Complete, want[i].Complete)
		}
	}
}
