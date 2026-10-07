package traversal

import (
	"fmt"
	"testing"
)

func deletionGraph() *Graph {
	graph := NewGraph()
	for _, id := range []string{"a", "b", "c"} {
		graph.PutObject(Object{ID: id})
	}
	graph.PutLink(Link{ID: "ab", FromID: "a", ToID: "b"})
	graph.PutLink(Link{ID: "bc", FromID: "b", ToID: "c"})
	return graph
}

func collectPages(t *testing.T, service *Service, initial PageRequest) []Page {
	t.Helper()
	page, err := service.Page(initial)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	pages := []Page{page}
	for page.NextCursor != "" {
		page, err = service.Page(PageRequest{
			Cursor:    page.NextCursor,
			BatchSize: initial.BatchSize,
			Mode:      initial.Mode,
		})
		if err != nil {
			t.Fatalf("next page: %v", err)
		}
		pages = append(pages, page)
	}
	return pages
}

func itemIDs(pages []Page) []string {
	var ids []string
	for _, page := range pages {
		for _, item := range page.Items {
			ids = append(ids, item.Object.ID)
		}
	}
	return ids
}

func assertIDSequence(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func TestCursorStableAfterConcurrentDeletion(t *testing.T) {
	graph := deletionGraph()
	service := NewService(graph)
	first, err := service.Page(PageRequest{StartObjectID: "a", BatchSize: 1, Mode: SilentDrop})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	graph.DeleteObject("b")

	second, err := service.Page(PageRequest{Cursor: first.NextCursor, BatchSize: 1, Mode: SilentDrop})
	if err != nil {
		t.Fatalf("second page after deletion: %v", err)
	}
	graph.DeleteLink("bc")
	graph.DeleteObject("c")
	third, err := service.Page(PageRequest{Cursor: second.NextCursor, BatchSize: 1, Mode: SilentDrop})
	if err != nil {
		t.Fatalf("third page after deletion: %v", err)
	}
	assertIDSequence(t, itemIDs([]Page{first, second, third}), []string{"a", "b", "c"})
	if second.Items[0].Link.ID != "ab" {
		t.Fatalf("deleted links were not preserved from snapshot: %#v %#v", second.Items[0], third.Items[0])
	}
	if third.Items[0].Link.ID != "bc" {
		t.Fatalf("deleted third link was not preserved from snapshot: %#v", third.Items[0])
	}
}

func truncationGraph() *Graph {
	graph := NewGraph()
	graph.PutObject(Object{ID: "root"})
	for _, id := range []string{"x1", "x2", "x3"} {
		graph.PutObject(Object{ID: id})
		graph.PutLink(Link{ID: "l-" + id, FromID: "root", ToID: id})
	}
	return graph
}

func TestSilentDropAndExplicitTruncationModes(t *testing.T) {
	for _, mode := range []Mode{SilentDrop, ExplicitTruncation} {
		service := NewService(truncationGraph())
		pages := collectPages(t, service, PageRequest{
			StartObjectID: "root",
			BatchSize:     2,
			Mode:          mode,
			HopLimits:     []int{2},
		})
		gotIDs := itemIDs(pages)
		assertIDSequence(t, gotIDs, []string{"root", "x1", "x2"})
		var markers []TruncationMarker
		for _, page := range pages {
			markers = append(markers, page.Truncations...)
		}
		if mode == SilentDrop && len(markers) != 0 {
			t.Fatalf("silent mode returned markers: %#v", markers)
		}
		if mode == ExplicitTruncation {
			if len(markers) != 1 || markers[0] != (TruncationMarker{Hop: 1, Dropped: 1}) {
				t.Fatalf("markers = %#v", markers)
			}
		}
	}
}

func TestInvalidUsedAndForeignCursorsRejected(t *testing.T) {
	graph := deletionGraph()
	service := NewService(graph)
	page, err := service.Page(PageRequest{StartObjectID: "a", BatchSize: 1, Mode: SilentDrop})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	if _, err := service.Page(PageRequest{Cursor: "not-a-cursor", BatchSize: 1, Mode: SilentDrop}); code(err) != ErrInvalidCursor {
		t.Fatalf("malformed cursor error = %v", err)
	}

	otherService := NewService(graph)
	if _, err := otherService.Page(PageRequest{Cursor: page.NextCursor, BatchSize: 1, Mode: SilentDrop}); code(err) != ErrInvalidCursor {
		t.Fatalf("foreign cursor error = %v", err)
	}

	if _, err := service.Page(PageRequest{Cursor: page.NextCursor, BatchSize: 1, Mode: SilentDrop}); err != nil {
		t.Fatalf("first use failed: %v", err)
	}
	if _, err := service.Page(PageRequest{Cursor: page.NextCursor, BatchSize: 1, Mode: SilentDrop}); code(err) != ErrInvalidCursor {
		t.Fatalf("reused cursor error = %v", err)
	}
}

func TestModeChangeRejectedAfterFirstRequest(t *testing.T) {
	service := NewService(deletionGraph())
	first, err := service.Page(PageRequest{StartObjectID: "a", BatchSize: 1, Mode: SilentDrop})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	_, err = service.Page(PageRequest{Cursor: first.NextCursor, BatchSize: 1, Mode: ExplicitTruncation})
	if code(err) != ErrModeChanged {
		t.Fatalf("mode change error = %v", err)
	}
}

func code(err error) string {
	if err == nil {
		return ""
	}
	if traversalErr, ok := err.(*TraversalError); ok {
		return traversalErr.Code
	}
	return err.Error()
}

func TestErrorOrder(t *testing.T) {
	service := NewService(deletionGraph())
	_, err := service.Page(PageRequest{StartObjectID: "missing", BatchSize: 0, Mode: Mode("bad")})
	if code(err) != ErrStartObjectNotFound {
		t.Fatalf("first-request order error = %v", err)
	}
	_, err = service.Page(PageRequest{Cursor: "bad", BatchSize: 0, Mode: Mode("bad")})
	if code(err) != ErrInvalidCursor {
		t.Fatalf("cursor order error = %v", err)
	}
	first, _ := service.Page(PageRequest{StartObjectID: "a", BatchSize: 1, Mode: SilentDrop})
	_, err = service.Page(PageRequest{Cursor: first.NextCursor, BatchSize: 0, Mode: ExplicitTruncation})
	if code(err) != ErrInvalidBatchSize {
		t.Fatalf("batch-before-mode order error = %v", err)
	}
}

func TestRequestLogRecordsCursorModeItemsAndMarkers(t *testing.T) {
	service := NewService(truncationGraph())
	first, err := service.Page(PageRequest{StartObjectID: "root", BatchSize: 2, Mode: ExplicitTruncation, HopLimits: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Page(PageRequest{Cursor: first.NextCursor, BatchSize: 2, Mode: ExplicitTruncation, HopLimits: []int{2}}); err != nil {
		t.Fatal(err)
	}
	logs := service.Logs()
	if len(logs) != 2 {
		t.Fatalf("logs len = %d", len(logs))
	}
	if logs[0].Cursor != "" || logs[0].Mode != ExplicitTruncation || len(logs[0].Items) != 2 {
		t.Fatalf("first log = %#v", logs[0])
	}
	if logs[1].Cursor != first.NextCursor || len(logs[1].Items) != 1 || fmt.Sprint(logs[1].Truncations) != "[{1 1}]" {
		t.Fatalf("second log = %#v", logs[1])
	}
	if len(logs[0].Truncations) != 0 {
		t.Fatalf("first log markers = %#v", logs[0].Truncations)
	}
}
