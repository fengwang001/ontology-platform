package orphanreclaim

import (
	"reflect"
	"sort"
	"testing"
)

// testConfig 覆盖：1 个独立保留类型 + 1 个两组联合保留类型。
// I  = independent：存在任意一条即保留
// J1/J2 = joint：二者入边须同时存在才共同保留；单独存在不保留
func testConfig() Config {
	return Config{
		Rules: map[string]LinkRule{
			"I":  {Type: "I", Kind: IndependentRetention},
			"J1": {Type: "J1", Kind: JointRetention, JointGroup: []string{"J2"}},
			"J2": {Type: "J2", Kind: JointRetention, JointGroup: []string{"J1"}},
			"X":  {Type: "X", Kind: JointRetention, JointGroup: []string{"Y"}},
			"Y":  {Type: "Y", Kind: JointRetention, JointGroup: []string{"X"}},
		},
		GraceGen1: 10,
		GraceGen2: 4, // 第二代更短
	}
}

type fakeClock struct{ t int64 }

func (c *fakeClock) now() int64 { return c.t }

type recLogger struct {
	decisions []DecisionRecord
	advances  []AdvanceRecord
}

func (l *recLogger) LogDecision(d DecisionRecord) { l.decisions = append(l.decisions, d) }
func (l *recLogger) LogAdvance(a AdvanceRecord)   { l.advances = append(l.advances, a) }

func newTestEngine(t *testing.T, cfg Config, t0 int64) (*Reclaimer, *fakeClock, *recLogger) {
	t.Helper()
	c := &fakeClock{t: t0}
	lg := &recLogger{}
	r, err := New(cfg, c.now, nil, lg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, c, lg
}

func mustAdd(t *testing.T, r *Reclaimer, src, tgt, lt string) {
	t.Helper()
	if err := r.AddLink(src, tgt, lt); err != nil {
		t.Fatalf("AddLink(%s->%s,%s): %v", src, tgt, lt, err)
	}
}

func mustRemove(t *testing.T, r *Reclaimer, src, tgt, lt string) {
	t.Helper()
	if err := r.RemoveLink(src, tgt, lt); err != nil {
		t.Fatalf("RemoveLink(%s->%s,%s): %v", src, tgt, lt, err)
	}
}

// injectEdgeNoReassess 仅供测试：直接改写入边索引而不触发即时重评，
// 用于构造「对象已在队列中、到期出堆时才发现判定翻转」的情形。
func injectEdgeNoReassess(r *Reclaimer, src, tgt, lt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.objects[tgt].addInEdge(src, lt)
	addTypeSet(r.objects[src].outLinks, tgt, lt)
}

func assertGen(t *testing.T, r *Reclaimer, id string, want Generation) {
	t.Helper()
	got, err := r.GenerationOf(id)
	if err != nil {
		t.Fatalf("GenerationOf(%s): %v", id, err)
	}
	if got != want {
		t.Fatalf("%s gen = %d, want %d", id, got, want)
	}
}

func assertGone(t *testing.T, r *Reclaimer, id string) {
	t.Helper()
	if _, err := r.GenerationOf(id); err != ErrObjectNotFound {
		t.Fatalf("%s: want ErrObjectNotFound, got %v", id, err)
	}
}

// cmpSnapshots 对两个快照逐字段比对（队列顺序已各自排序）。
func cmpSnapshots(t *testing.T, tag string, got, want Snapshot) {
	t.Helper()
	if got.At != want.At {
		t.Fatalf("%s: At %d != %d", tag, got.At, want.At)
	}
	if !reflect.DeepEqual(got.Alive, want.Alive) {
		t.Fatalf("%s: Alive mismatch\n got %v\nwant %v", tag, got.Alive, want.Alive)
	}
	if !reflect.DeepEqual(got.Since, want.Since) {
		t.Fatalf("%s: Since mismatch\n got %v\nwant %v", tag, got.Since, want.Since)
	}
	if !reflect.DeepEqual(got.Deadline, want.Deadline) {
		t.Fatalf("%s: Deadline mismatch\n got %v\nwant %v", tag, got.Deadline, want.Deadline)
	}
	if !reflect.DeepEqual(got.InEdges, want.InEdges) {
		t.Fatalf("%s: InEdges mismatch\n got %v\nwant %v", tag, got.InEdges, want.InEdges)
	}
	if !reflect.DeepEqual(got.OutEdges, want.OutEdges) {
		t.Fatalf("%s: OutEdges mismatch\n got %v\nwant %v", tag, got.OutEdges, want.OutEdges)
	}
	sort.Strings(got.Gen1Queue)
	sort.Strings(want.Gen1Queue)
	sort.Strings(got.Gen2Queue)
	sort.Strings(want.Gen2Queue)
	if !reflect.DeepEqual(got.Gen1Queue, want.Gen1Queue) {
		t.Fatalf("%s: Gen1Queue %v != %v", tag, got.Gen1Queue, want.Gen1Queue)
	}
	if !reflect.DeepEqual(got.Gen2Queue, want.Gen2Queue) {
		t.Fatalf("%s: Gen2Queue %v != %v", tag, got.Gen2Queue, want.Gen2Queue)
	}
}
