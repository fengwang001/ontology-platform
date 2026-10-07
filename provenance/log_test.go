package provenance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// 每次查询在日志中记录输入参数、返回路径集合与每条路径的三方记录标识。
func TestQueryLoggerContents(t *testing.T) {
	s := buildDemoGraph(t)
	var buf bytes.Buffer
	logger := NewQueryLogger(&buf)

	q := Query{Source: "A", ValidAt: 150, AsOf: 50, MaxDepth: 3}
	res, err := s.Traverse(q)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(q, res); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	var entry LogEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("log line not valid JSON: %v (%s)", err, line)
	}
	if entry.Source != "A" || entry.ValidAt != 150 || entry.AsOf != 50 || entry.MaxDepth != 3 {
		t.Fatalf("log input params wrong: %+v", entry)
	}
	if entry.SourceRecord.ID != "A" || entry.SourceRecord.Kind != "object" {
		t.Fatalf("log source record wrong: %+v", entry.SourceRecord)
	}
	if len(entry.Paths) != len(res.Paths) {
		t.Fatalf("log paths count %d want %d", len(entry.Paths), len(res.Paths))
	}
	// 每条路径的每一跳都必须带链接与目标两个三方记录标识。
	for _, p := range entry.Paths {
		for _, h := range p.Hops {
			if h.Link.Kind != "link" || h.Link.ID == "" || h.Link.Seq <= 0 {
				t.Fatalf("hop link ref incomplete: %+v", h)
			}
			if h.Target.Kind != "object" || h.Target.ID == "" || h.Target.Seq <= 0 {
				t.Fatalf("hop target ref incomplete: %+v", h)
			}
		}
	}
	if entry.CandidatesSeen != res.CandidatesSeen || entry.CandidatesSeen == 0 {
		t.Fatalf("candidates seen wrong: %d", entry.CandidatesSeen)
	}
}

// 错误查询也被记录，且每条日志恰好一行（交织时不撕裂）。
func TestQueryLoggerErrorsAndLines(t *testing.T) {
	s := NewStore()
	var buf bytes.Buffer
	logger := NewQueryLogger(&buf)

	queries := []Query{
		{Source: "X", ValidAt: 1, AsOf: 1, MaxDepth: 1},
		{Source: "X", ValidAt: IllegalTime, AsOf: 1, MaxDepth: 1},
	}
	for _, q := range queries {
		_, err := s.Traverse(q)
		if err == nil {
			t.Fatal("expected error")
		}
		if err := logger.LogError(q, err); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 log lines, got %d", len(lines))
	}
	for i, line := range lines {
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
		if payload["error"] == nil {
			t.Fatalf("line %d missing error field", i)
		}
	}
}
