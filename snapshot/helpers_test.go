package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// sliceLogger 记录每次判定的输入、输出与依据；测试打印其内容，
// 满足“日志须打印每次判定的输入、输出与依据”。
type sliceLogger struct {
	mu        sync.Mutex
	decisions []Decision
}

func (l *sliceLogger) Log(d Decision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.decisions = append(l.decisions, d)
}

func (l *sliceLogger) print(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, d := range l.decisions {
		where := ""
		if d.Chunk != nil {
			where = fmt.Sprintf(" chunk=%s/%d", d.Chunk.Type, d.Chunk.Chunk)
		}
		t.Logf("[decision] stage=%s%s | input=%s | output=%s | basis=%s",
			d.Stage, where, d.Input, d.Output, d.Basis)
	}
}

func mustExport(t *testing.T, dir string, req ExportRequest) {
	t.Helper()
	if err := Export(dir, req, &sliceLogger{}); err != nil {
		t.Fatalf("export: %v", err)
	}
}

func loadChunkFile(t *testing.T, dir, typ string, idx int) *Envelope {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, chunkFileName(typ, idx)))
	if err != nil {
		t.Fatalf("read chunk: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}
	return &env
}

func writeChunkFile(t *testing.T, dir, typ string, idx int, env *Envelope) {
	t.Helper()
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, chunkFileName(typ, idx)), raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func issueKinds(issues []Issue) []IssueKind {
	out := make([]IssueKind, len(issues))
	for i, is := range issues {
		out[i] = is.Kind
	}
	return out
}

func readFileForTest(path string) ([]byte, error) {
	return os.ReadFile(path)
}
