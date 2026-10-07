package traversal

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// MemoryLogger 并发安全地在内存中保留全部日志条目，主要用于测试断言。
type MemoryLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

// NewMemoryLogger 创建内存日志记录器。
func NewMemoryLogger() *MemoryLogger {
	return &MemoryLogger{}
}

// LogEntry 实现 Logger。
func (l *MemoryLogger) LogEntry(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

// Entries 返回已记录日志条目的副本。
func (l *MemoryLogger) Entries() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// TextLogger 将日志条目以稳定的一行式文本写入指定 Writer（并发安全）。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger 创建文本日志记录器；w 为 nil 时写入标准错误。
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stderr
	}
	return &TextLogger{w: w}
}

// LogEntry 实现 Logger。
func (l *TextLogger) LogEntry(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "traversal start=%q max_depth=%d directions=%s accepted=%t",
		entry.Start, entry.MaxDepth, formatDirections(entry.Directions), entry.Accepted)
	if !entry.Accepted {
		fmt.Fprintf(&b, " error=%q\n", entry.Error)
		_, _ = l.w.Write([]byte(b.String()))
		return
	}
	fmt.Fprintf(&b, " snapshot=%d paths=%d stats={expanded=%d edges=%d checks=%d probes=%d}\n",
		entry.SnapshotVersion, len(entry.Paths),
		entry.Stats.ExpandedNodes, entry.Stats.CandidateEdges,
		entry.Stats.AncestorChecks, entry.Stats.AncestorProbes)
	for _, p := range entry.Paths {
		fmt.Fprintf(&b, "  path#%d status=%s depth=%d nodes=%s",
			p.PathIndex, p.Status, p.Depth, formatNodes(p.Nodes))
		if p.Status == StatusCycle {
			fmt.Fprintf(&b, " cycle={repeated=%q ancestor_index=%d ancestors=%s}",
				p.CycleRepeated, p.CycleAncestorIdx, formatNodes(p.CycleAncestors))
		}
		b.WriteByte('\n')
	}
	_, _ = l.w.Write([]byte(b.String()))
}

func formatDirections(dir map[LinkTypeID]Direction) string {
	if len(dir) == 0 {
		return "{}"
	}
	types := make([]LinkTypeID, 0, len(dir))
	for t, d := range dir {
		_ = d
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, fmt.Sprintf("%s:%s", t, dir[t]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatNodes(nodes []ObjectID) string {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = string(n)
	}
	return "[" + strings.Join(parts, "->") + "]"
}
