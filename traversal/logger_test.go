package traversal

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestLoggingRecordsInputsStatusesAndAncestors 日志须包含输入、每条路径的
// 终态分类与据以判定的祖先序列。
func TestLoggingRecordsInputsStatusesAndAncestors(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "ab", tRel, "A", "B")
	mustAddLink(t, g, "bc", tRel, "B", "C")
	mustAddLink(t, g, "ca", tRel, "C", "A") // A-B-C-A 环路

	logger := NewMemoryLogger()
	svc := NewService(g, logger)
	req := TraversalRequest{Start: "A", Directions: dirs(DirOutbound), MaxDepth: 5}
	res, err := svc.Traverse(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	entries := logger.Entries()
	if len(entries) != 1 {
		t.Fatalf("want exactly one log entry, got %d", len(entries))
	}
	e := entries[0]
	if !e.Accepted || e.Start != "A" || e.MaxDepth != 5 {
		t.Fatalf("entry input fields wrong: %#v", e)
	}
	if e.Directions[tRel] != DirOutbound {
		t.Fatalf("directions not logged: %#v", e.Directions)
	}
	if e.SnapshotVersion != res.SnapshotVersion {
		t.Fatalf("logged snapshot version mismatch")
	}
	if len(e.Paths) != len(res.Paths) {
		t.Fatalf("logged path count mismatch")
	}

	var foundCycle bool
	for _, p := range e.Paths {
		if p.Status != StatusCycle {
			continue
		}
		foundCycle = true
		if p.CycleRepeated != "A" || p.CycleAncestorIdx != 0 {
			t.Fatalf("cycle log fields wrong: %#v", p)
		}
		wantAncestors := []ObjectID{"A", "B", "C"}
		if len(p.CycleAncestors) != 3 {
			t.Fatalf("ancestor sequence length wrong: %v", p.CycleAncestors)
		}
		for i := range wantAncestors {
			if p.CycleAncestors[i] != wantAncestors[i] {
				t.Fatalf("ancestor sequence wrong: %v", p.CycleAncestors)
			}
		}
	}
	if !foundCycle {
		t.Fatalf("at least one cycle path must be logged: %#v", e.Paths)
	}
	if e.Stats.AncestorProbes != e.Stats.CandidateEdges {
		t.Fatalf("stats not logged correctly: %#v", e.Stats)
	}
}

// TestRejectedRequestLoggedWithoutPaths 被拒绝的请求也要记录，但无路径、
// 无快照版本（未进入遍历）。
func TestRejectedRequestLoggedWithoutPaths(t *testing.T) {
	logger := NewMemoryLogger()
	svc := NewService(NewGraph(), logger)
	_, _ = svc.Traverse(context.Background(), TraversalRequest{
		Start: "ghost", Directions: dirs(DirOutbound), MaxDepth: 3,
	})
	e := logger.Entries()[0]
	if e.Accepted || e.Error != ErrStartObjectNotFound.Error() || len(e.Paths) != 0 {
		t.Fatalf("rejected log entry wrong: %#v", e)
	}
}

// TestTextLoggerOutput 文本日志包含终态与祖先序列的可读记录。
func TestTextLoggerOutput(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "self", tRel, "A", "A")

	var buf bytes.Buffer
	svc := NewService(g, NewTextLogger(&buf))
	if _, err := svc.Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 3,
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "status=cycle") || !strings.Contains(out, "repeated=\"A\"") {
		t.Fatalf("text log missing cycle evidence:\n%s", out)
	}
	if !strings.Contains(out, "ancestors=[A]") {
		t.Fatalf("text log missing ancestor sequence:\n%s", out)
	}
}
