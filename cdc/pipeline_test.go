package cdc

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// testWriter 把组件日志接入 testing 日志，便于在 -v 下观察
// 输入、拆分与分区输出及判定依据。
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func newTestPipeline(t *testing.T, numParts, maxBatch int) *Pipeline {
	t.Helper()
	return New(numParts, maxBatch, log.New(testWriter{t}, "[cdc] ", 0))
}

// applyAndCheck 应用一批变更并做通用断言：下游视图与源表一致、
// 各分区内事件按拆分序号有序。
func applyAndCheck(t *testing.T, p *Pipeline, batch []Change) [][]Event {
	t.Helper()
	parts, err := p.Apply(batch)
	if err != nil {
		t.Fatalf("Apply 意外失败: %v", err)
	}
	assertConsistent(t, p)
	for i, part := range parts {
		for j := 1; j < len(part); j++ {
			if part[j].Seq <= part[j-1].Seq {
				t.Fatalf("分区 %d 内事件乱序: %v", i, part)
			}
		}
	}
	return parts
}

func assertConsistent(t *testing.T, p *Pipeline) {
	t.Helper()
	snap := p.Snapshot()
	if !reflect.DeepEqual(snap.Source, snap.Downstream) {
		t.Fatalf("下游视图与源表不一致:\nsource=%v\ndownstream=%v", snap.Source, snap.Downstream)
	}
}

func flatten(parts [][]Event) []Event {
	var out []Event
	for _, part := range parts {
		out = append(out, part...)
	}
	// 事件可能分散在不同分区，按拆分序号重排以校验全局序列。
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func TestChainedPrimaryKeyChanges(t *testing.T) {
	p := newTestPipeline(t, 4, 16)

	applyAndCheck(t, p, []Change{
		{Kind: Insert, Key: "A", Row: Row{"v": "1"}},
	})

	// 同一批内链式换键: A -> B -> C。
	// 拆分序列: DEL A, WRITE B, DEL B, WRITE C；
	// 合并后 B 只保留最后的 DEL B。
	parts := applyAndCheck(t, p, []Change{
		{Kind: Update, OldKey: "A", Key: "B", Row: Row{"v": "2"}},
		{Kind: Update, OldKey: "B", Key: "C", Row: Row{"v": "3"}},
	})
	got := flatten(parts)
	want := []Event{
		{Seq: 0, Op: OpDelete, Key: "A"},
		{Seq: 2, Op: OpDelete, Key: "B"},
		{Seq: 3, Op: OpWrite, Key: "C", Row: Row{"v": "3"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("链式主键变更合并结果不符:\ngot  %v\nwant %v", got, want)
	}

	// 跨批继续链式换键: C -> D。
	parts = applyAndCheck(t, p, []Change{
		{Kind: Update, OldKey: "C", Key: "D", Row: Row{"v": "4"}},
	})
	got = flatten(parts)
	want = []Event{
		{Seq: 0, Op: OpDelete, Key: "C"},
		{Seq: 1, Op: OpWrite, Key: "D", Row: Row{"v": "4"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("跨批链式换键结果不符:\ngot  %v\nwant %v", got, want)
	}

	snap := p.Snapshot()
	if !reflect.DeepEqual(snap.Source, map[string]Row{"D": {"v": "4"}}) {
		t.Fatalf("源表终态不符: %v", snap.Source)
	}
}

func TestUpdateWithoutPrimaryKeyChange(t *testing.T) {
	p := newTestPipeline(t, 4, 16)

	applyAndCheck(t, p, []Change{{Kind: Insert, Key: "k1", Row: Row{"v": "1"}}})

	// 主键不变的更新只产生一次写入，不得产生删除。
	parts := applyAndCheck(t, p, []Change{
		{Kind: Update, Key: "k1", Row: Row{"v": "2"}},
	})
	got := flatten(parts)
	want := []Event{{Seq: 0, Op: OpWrite, Key: "k1", Row: Row{"v": "2"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("主键不变的更新应只产生一次写入:\ngot  %v\nwant %v", got, want)
	}
	for _, ev := range got {
		if ev.Op == OpDelete {
			t.Fatalf("主键不变的更新不应产生删除事件: %v", ev)
		}
	}
}

func TestUpdateWithPrimaryKeyChange(t *testing.T) {
	p := newTestPipeline(t, 4, 16)

	applyAndCheck(t, p, []Change{{Kind: Insert, Key: "old", Row: Row{"v": "1"}}})

	// 主键变化的更新必须先删除旧键再写入新键。
	parts := applyAndCheck(t, p, []Change{
		{Kind: Update, OldKey: "old", Key: "new", Row: Row{"v": "2"}},
	})
	got := flatten(parts)
	want := []Event{
		{Seq: 0, Op: OpDelete, Key: "old"},
		{Seq: 1, Op: OpWrite, Key: "new", Row: Row{"v": "2"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("主键变化的更新应先删后写:\ngot  %v\nwant %v", got, want)
	}
}

func TestInsertDeleteMapping(t *testing.T) {
	p := newTestPipeline(t, 4, 16)

	parts := applyAndCheck(t, p, []Change{{Kind: Insert, Key: "k", Row: Row{"v": "1"}}})
	if got := flatten(parts); !reflect.DeepEqual(got, []Event{{Seq: 0, Op: OpWrite, Key: "k", Row: Row{"v": "1"}}}) {
		t.Fatalf("插入应映射为写入: %v", got)
	}

	parts = applyAndCheck(t, p, []Change{{Kind: Delete, Key: "k"}})
	if got := flatten(parts); !reflect.DeepEqual(got, []Event{{Seq: 0, Op: OpDelete, Key: "k"}}) {
		t.Fatalf("删除应映射为删除: %v", got)
	}
}

func TestMergeKeepsLastEventPerKey(t *testing.T) {
	p := newTestPipeline(t, 4, 16)

	// 同批内先插入再更新同一键：只保留最后的写入。
	parts := applyAndCheck(t, p, []Change{
		{Kind: Insert, Key: "k", Row: Row{"v": "1"}},
		{Kind: Update, Key: "k", Row: Row{"v": "2"}},
	})
	got := flatten(parts)
	want := []Event{{Seq: 1, Op: OpWrite, Key: "k", Row: Row{"v": "2"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("同一键只应保留拆分序列中的最后一条:\ngot  %v\nwant %v", got, want)
	}

	// 同批内先插入再删除同一键：只保留删除。
	parts = applyAndCheck(t, p, []Change{
		{Kind: Insert, Key: "tmp", Row: Row{"v": "x"}},
		{Kind: Delete, Key: "tmp"},
	})
	got = flatten(parts)
	want = []Event{{Seq: 1, Op: OpDelete, Key: "tmp"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("插入后删除应只保留删除:\ngot  %v\nwant %v", got, want)
	}
}

// assertRejected 断言批被拒绝、原因可区分且状态零副作用。
func assertRejected(t *testing.T, p *Pipeline, batch []Change, reason error) {
	t.Helper()
	before := p.Snapshot()
	_, err := p.Apply(batch)
	if err == nil {
		t.Fatalf("非法批应被拒绝: %v", batch)
	}
	if !errors.Is(err, reason) {
		t.Fatalf("拒绝原因不符: got %v, want %v", err, reason)
	}
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("错误应携带 *RejectError 定位信息: %v", err)
	}
	t.Logf("判定依据: %v", rej)
	after := p.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒绝的批不得改变任何状态:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestRejectInvalidPrimaryKey(t *testing.T) {
	p := newTestPipeline(t, 4, 16)
	assertRejected(t, p, []Change{{Kind: Insert, Key: "", Row: Row{"v": "1"}}}, ErrInvalidPrimaryKey)
	assertRejected(t, p, []Change{{Kind: Insert, Key: strings.Repeat("x", maxKeyLength+1)}}, ErrInvalidPrimaryKey)
	assertRejected(t, p, []Change{{Kind: Delete, Key: ""}}, ErrInvalidPrimaryKey)
}

func TestRejectKeyAlreadyExists(t *testing.T) {
	p := newTestPipeline(t, 4, 16)
	applyAndCheck(t, p, []Change{{Kind: Insert, Key: "k", Row: Row{"v": "1"}}})

	// 跨批插入同名键。
	assertRejected(t, p, []Change{{Kind: Insert, Key: "k", Row: Row{"v": "2"}}}, ErrKeyAlreadyExists)
	// 同批内重复插入。
	assertRejected(t, p, []Change{
		{Kind: Insert, Key: "x", Row: Row{"v": "1"}},
		{Kind: Insert, Key: "x", Row: Row{"v": "2"}},
	}, ErrKeyAlreadyExists)
	// 主键变更后撞上已存在的键。
	assertRejected(t, p, []Change{
		{Kind: Insert, Key: "y", Row: Row{"v": "1"}},
		{Kind: Update, OldKey: "k", Key: "y", Row: Row{"v": "2"}},
	}, ErrKeyAlreadyExists)
}

func TestRejectKeyNotFound(t *testing.T) {
	p := newTestPipeline(t, 4, 16)
	assertRejected(t, p, []Change{{Kind: Update, Key: "ghost", Row: Row{"v": "1"}}}, ErrKeyNotFound)
	assertRejected(t, p, []Change{{Kind: Delete, Key: "ghost"}}, ErrKeyNotFound)
	assertRejected(t, p, []Change{{Kind: Update, OldKey: "ghost", Key: "new", Row: Row{"v": "1"}}}, ErrKeyNotFound)
}

func TestRejectTooManyRows(t *testing.T) {
	p := newTestPipeline(t, 4, 3)
	batch := []Change{
		{Kind: Insert, Key: "a", Row: Row{"v": "1"}},
		{Kind: Insert, Key: "b", Row: Row{"v": "2"}},
		{Kind: Insert, Key: "c", Row: Row{"v": "3"}},
		{Kind: Insert, Key: "d", Row: Row{"v": "4"}},
	}
	assertRejected(t, p, batch, ErrTooManyRows)
}

func TestDeterministic(t *testing.T) {
	script := [][]Change{
		{{Kind: Insert, Key: "a", Row: Row{"v": "1"}}, {Kind: Insert, Key: "b", Row: Row{"v": "1"}}},
		{{Kind: Update, OldKey: "a", Key: "c", Row: Row{"v": "2"}}},
		{{Kind: Update, Key: "b", Row: Row{"v": "9"}}, {Kind: Delete, Key: "c"}},
	}
	run := func() Snapshot {
		p := New(8, 16, nil)
		var prev [][]Event
		for _, batch := range script {
			parts, err := p.Apply(batch)
			if err != nil {
				t.Fatalf("Apply 意外失败: %v", err)
			}
			if prev != nil && reflect.DeepEqual(prev, parts) && len(flatten(parts)) == 0 {
				t.Fatalf("空输出不应重复")
			}
			prev = parts
		}
		return p.Snapshot()
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(first, got) {
			t.Fatalf("同一输入序列反复计算结果不一致:\nfirst=%v\ngot=%v", first, got)
		}
	}
	t.Logf("确定性输出: %+v", first)
}

func TestPartitionStability(t *testing.T) {
	const parts = 8
	seen := make(map[string]int)
	for i := 0; i < 200; i++ {
		key := fmt.Sprintf("key-%d", i)
		got := partitionOf(key, parts)
		if got < 0 || got >= parts {
			t.Fatalf("分区下标越界: key=%q part=%d", key, got)
		}
		if prev, ok := seen[key]; ok && prev != got {
			t.Fatalf("同一主键映射到不同分区: key=%q %d != %d", key, prev, got)
		}
		seen[key] = got
		if again := partitionOf(key, parts); again != got {
			t.Fatalf("分区函数不稳定: key=%q %d != %d", key, got, again)
		}
	}
}

func TestConcurrentSnapshotConsistency(t *testing.T) {
	p := newTestPipeline(t, 8, 16)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 并发读者：任何时候读到的下游视图都必须与源表快照相等。
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := p.Snapshot()
				if !reflect.DeepEqual(snap.Source, snap.Downstream) {
					t.Errorf("并发读到不一致快照:\nsource=%v\ndownstream=%v", snap.Source, snap.Downstream)
					return
				}
			}
		}()
	}

	// 单个写者顺序应用包含主键变更的批。
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("k-%d", i)
		if _, err := p.Apply([]Change{{Kind: Insert, Key: key, Row: Row{"v": "0"}}}); err != nil {
			t.Fatalf("Apply 意外失败: %v", err)
		}
		if _, err := p.Apply([]Change{
			{Kind: Update, OldKey: key, Key: key + "-m", Row: Row{"v": "1"}},
			{Kind: Update, Key: key + "-m", Row: Row{"v": "2"}},
		}); err != nil {
			t.Fatalf("Apply 意外失败: %v", err)
		}
	}
	close(stop)
	wg.Wait()

	snap := p.Snapshot()
	if len(snap.Source) != 50 {
		t.Fatalf("源表行数不符: got %d, want 50", len(snap.Source))
	}
	if !reflect.DeepEqual(snap.Source, snap.Downstream) {
		t.Fatalf("终态下游视图与源表不一致")
	}
}
