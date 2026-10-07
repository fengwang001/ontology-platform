package ontology

import (
	"fmt"
	"strings"
)

// TraversalRecord is one structured log entry.
type TraversalRecord struct {
	Request TraverseRequest
	Result  TraverseResult
}

// MemoryLogger keeps all traversal records in memory and optionally mirrors
// them to a string builder. It is safe for concurrent use.
type MemoryLogger struct {
	records []TraversalRecord
}

// NewMemoryLogger constructs a MemoryLogger.
func NewMemoryLogger() *MemoryLogger { return &MemoryLogger{} }

// LogTraversal implements Logger.
func (l *MemoryLogger) LogTraversal(req TraverseRequest, result TraverseResult) {
	l.records = append(l.records, TraversalRecord{Request: req, Result: cloneResult(result)})
}

// Records returns all logged records in traversal order.
func (l *MemoryLogger) Records() []TraversalRecord {
	out := make([]TraversalRecord, len(l.records))
	copy(out, l.records)
	return out
}

// Render produces a human-readable rendering of every record: input, the
// terminal classification of each path and the ancestor sequence used for
// the decision.
func (l *MemoryLogger) Render() string {
	var sb strings.Builder
	for i, rec := range l.records {
		fmt.Fprintf(&sb, "traversal #%d start=%s maxDepth=%d directions=%s ancestorChecks=%d\n",
			i+1, rec.Request.Start, rec.Request.MaxDepth, renderDirections(rec.Request.LinkTypes), rec.Result.AncestorChecks)
		for _, p := range rec.Result.Paths {
			fmt.Fprintf(&sb, "  path=%s links=%s terminal=%s ancestors=%s\n",
				renderObjects(p.Nodes), renderLinks(p.Links), p.Reason, renderObjects(p.Ancestors))
		}
	}
	return sb.String()
}

func cloneResult(in TraverseResult) TraverseResult {
	out := TraverseResult{AncestorChecks: in.AncestorChecks, Paths: make([]Path, len(in.Paths))}
	for i, p := range in.Paths {
		out.Paths[i] = Path{
			Nodes:     append([]ObjectID(nil), p.Nodes...),
			Links:     append([]LinkID(nil), p.Links...),
			Reason:    p.Reason,
			Ancestors: append([]ObjectID(nil), p.Ancestors...),
		}
	}
	return out
}

func renderObjects(seq []ObjectID) string {
	parts := make([]string, len(seq))
	for i, o := range seq {
		parts[i] = string(o)
	}
	return "[" + strings.Join(parts, " -> ") + "]"
}

func renderLinks(seq []LinkID) string {
	parts := make([]string, len(seq))
	for i, o := range seq {
		parts[i] = string(o)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func renderDirections(d map[LinkType]Direction) string {
	parts := make([]string, 0, len(d))
	for t, dir := range d {
		name := "out"
		if dir == DirectionIn {
			name = "in"
		}
		parts = append(parts, string(t)+":"+name)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
