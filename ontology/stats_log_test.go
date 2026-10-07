package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestAncestorCheckBound 验证完整图环路判定的祖先序列核对次数
// 不随图中对象、链接与权限标签种类总数的增长而线性增长：
// 在遍历形状完全相同、但总量成倍增长的图上，核对次数必须保持不变。
func TestAncestorCheckBound(t *testing.T) {
	build := func(scale int) *Store {
		s := NewStore()
		// 固定的遍历形状：A -> B -> C -> D（全部 public）。
		for i, l := range []Link{
			{ID: "c0", From: "A", To: "B", Label: "public"},
			{ID: "c1", From: "B", To: "C", Label: "public"},
			{ID: "c2", From: "C", To: "D", Label: "public"},
		} {
			_ = i
			if err := s.AddLink(l); err != nil {
				t.Fatal(err)
			}
		}
		// 填充规模：scale*100 个额外对象、scale*100 条额外链接、
		// scale*10 种额外权限标签，全部位于遍历不可达的分量中。
		for i := 0; i < scale*100; i++ {
			l := Link{
				ID:    fmt.Sprintf("p%d", i),
				From:  ObjectID(fmt.Sprintf("x%d", i)),
				To:    ObjectID(fmt.Sprintf("x%d", i+1)),
				Label: Label(fmt.Sprintf("pad-label-%d", i%max(1, scale*10))),
			}
			if err := s.AddLink(l); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	req := Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 10}
	var baseline *Stats
	for _, scale := range []int{0, 1, 2, 4, 8} {
		res, err := Traverse(build(scale), req, nil)
		if err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			s := res.Stats
			baseline = &s
			continue
		}
		if res.Stats.AncestorChecks != baseline.AncestorChecks {
			t.Fatalf("ancestor checks grew with graph size: scale=0 -> %d, scale -> %d",
				baseline.AncestorChecks, res.Stats.AncestorChecks)
		}
	}
	// 每条被考察的链接恰好核对一次：核对次数与扩展链接数相等。
	if baseline.AncestorChecks != baseline.ExpandedLinks || baseline.AncestorChecks != 3 {
		t.Fatalf("unexpected stats: %+v", baseline)
	}
}

// TestAncestorCheckPerHopConstant 验证祖先核对次数随路径深度线性增长
// （每跳恰好一次），而不是随深度平方增长（如线性扫描祖先序列）。
func TestAncestorCheckPerHopConstant(t *testing.T) {
	depths := []int{4, 8, 16, 32}
	for _, d := range depths {
		s := NewStore()
		for i := 0; i < d; i++ {
			l := Link{
				ID:    fmt.Sprintf("e%d", i),
				From:  ObjectID(fmt.Sprintf("n%d", i)),
				To:    ObjectID(fmt.Sprintf("n%d", i+1)),
				Label: "public",
			}
			if err := s.AddLink(l); err != nil {
				t.Fatal(err)
			}
		}
		res, err := Traverse(s, Request{
			CallerLabels: []Label{"public"}, Start: "n0", MaxDepth: d + 1,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Stats.AncestorChecks != int64(d) {
			t.Fatalf("depth %d: ancestor checks = %d, want %d (one per hop)",
				d, res.Stats.AncestorChecks, d)
		}
	}
}

// memLogger 是测试用的内存日志记录器。
type memLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

func (m *memLogger) Log(e LogEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
}

// TestTraversalLogging 验证每次遍历在日志中记录调用方权限、输入
// 与各条路径的判定依据。
func TestTraversalLogging(t *testing.T) {
	s := buildStore(t,
		Link{ID: "l1", From: "A", To: "B", Label: "public"},
		Link{ID: "l2", From: "B", To: "A", Label: "secret"},
		Link{ID: "l3", From: "B", To: "C", Label: "secret"},
		Link{ID: "l4", From: "A", To: "D", Label: "public"},
	)
	req := Request{CallerLabels: []Label{"public"}, Start: "A", MaxDepth: 5}
	logger := &memLogger{}
	if _, err := Traverse(s, req, logger); err != nil {
		t.Fatal(err)
	}
	if len(logger.entries) == 0 {
		t.Fatal("no log entries recorded")
	}
	// 每条日志都必须携带调用方权限与输入。
	for _, e := range logger.entries {
		if len(e.CallerLabels) != 1 || e.CallerLabels[0] != "public" {
			t.Fatalf("log entry missing caller labels: %+v", e)
		}
		if e.Start != "A" || e.MaxDepth != 5 {
			t.Fatalf("log entry missing request inputs: %+v", e)
		}
	}
	// 必须存在 begin/end 记录。
	if logger.entries[0].Kind != DecisionBegin {
		t.Fatalf("first entry = %v, want begin", logger.entries[0].Kind)
	}
	if logger.entries[len(logger.entries)-1].Kind != DecisionEnd {
		t.Fatalf("last entry = %v, want end", logger.entries[len(logger.entries)-1].Kind)
	}
	// 各条路径的判定依据：扩展、隐藏环路、部分不可见三类判定都必须出现，
	// 且带有判定位置、路径前缀与可读理由。
	kinds := map[DecisionKind]LogEntry{}
	for _, e := range logger.entries {
		if _, ok := kinds[e.Kind]; !ok {
			kinds[e.Kind] = e
		}
	}
	for _, k := range []DecisionKind{DecisionExtended, DecisionCycle, DecisionHidden} {
		e, ok := kinds[k]
		if !ok {
			t.Fatalf("missing decision kind %q in log", k)
		}
		if e.Reason == "" {
			t.Fatalf("decision %q has no reason", k)
		}
	}
	// 环路判定发生在 B，路径前缀为 [l1]。
	cycle := kinds[DecisionCycle]
	if cycle.At != "B" || len(cycle.Path) != 1 || cycle.Path[0] != "l1" {
		t.Fatalf("cycle decision context wrong: %+v", cycle)
	}
	// 隐藏环路判定不得泄露被隐藏链接的 ID。
	if cycle.LinkID != "" {
		t.Fatalf("cycle decision leaks hidden link ID: %+v", cycle)
	}
}
