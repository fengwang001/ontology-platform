package join

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

var scenario = []Op{
	rOp(Insert, "r1", "k1", "a"),
	lOp(Insert, "l2", "k1", "b"),
	lOp(Insert, "l1", "k2", "c"),
	rOp(Insert, "r3", "k1", "d"),
	rOp(Insert, "r2", "k2", "e"),
	lOp(Insert, "l3", "k1", "f"),
	rOp(Delete, "r1", "k1", "a"),
	lOp(Delete, "l3", "k1", "f"),
	rOp(Insert, "r4", "k2", "g"),
}

func runScenario() ([]Entry, []Entry, []Decision) {
	j := New(0)
	var emitted []Entry
	for _, op := range scenario {
		res, err := j.Apply([]Op{op})
		if err != nil {
			panic(err)
		}
		emitted = append(emitted, res.Entries...)
	}
	return emitted, j.Snapshot(), j.DecisionLog()
}

// TestDeterministicReplay 同一输入序列反复计算得到完全相同的输出，
// 且下游按顺序应用日志后得到的视图与快照一致。
func TestDeterministicReplay(t *testing.T) {
	first, firstSnap, firstLog := runScenario()
	for iter := 0; iter < 5; iter++ {
		emitted, snap, log := runScenario()
		if fmt.Sprint(emitted) != fmt.Sprint(first) {
			t.Fatalf("第 %d 次运行的变更日志与首次不一致", iter)
		}
		if fmt.Sprint(snap) != fmt.Sprint(firstSnap) {
			t.Fatalf("第 %d 次运行的快照与首次不一致", iter)
		}
		if len(log) != len(firstLog) {
			t.Fatalf("第 %d 次运行的判定日志条数不一致", iter)
		}
		for i := range log {
			if fmt.Sprint(log[i]) != fmt.Sprint(firstLog[i]) {
				t.Fatalf("判定日志第 %d 条跨运行不一致", i)
			}
		}
	}

	// 下游按顺序应用全部日志条目，应得到最终快照。
	view := map[string]Entry{}
	replayToView(view, first)
	if len(view) != len(firstSnap) {
		t.Fatalf("重放视图行数 %d != 快照行数 %d", len(view), len(firstSnap))
	}
	for _, e := range firstSnap {
		if view[entryKey(e)] != e {
			t.Fatalf("重放视图缺少或不一致: %+v", e)
		}
	}

	// 最终视图内容断言：k1 仅 r3 存活 -> l2 与 r3 配对；k2 有 r2,r4 -> l1 两条配对。
	if _, ok := view["l2|r3"]; !ok {
		t.Fatalf("l2 应与 r3 配对，view=%v", view)
	}
	if _, ok := view["l2|"]; ok {
		t.Fatalf("l2 存在同键右行 r3，不应有空填充行")
	}
	if _, ok := view["l1|r2"]; !ok {
		t.Fatalf("缺少配对 l1|r2")
	}
	if _, ok := view["l1|r4"]; !ok {
		t.Fatalf("缺少配对 l1|r4")
	}
	if _, ok := view["l1|"]; ok {
		t.Fatalf("l1 有同键右行，不应存在空填充行")
	}
}

// TestConcurrentReaders 写入过程中并发读取，每次快照都必须逐行一致、排序稳定。
func TestConcurrentReaders(t *testing.T) {
	j := New(0)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := j.Snapshot()
					for i := 1; i < len(snap); i++ {
						prev, cur := snap[i-1], snap[i]
						if prev.LeftID > cur.LeftID ||
							(prev.LeftID == cur.LeftID && prev.RightID > cur.RightID) {
							t.Errorf("快照顺序不一致: %q before %q", entryKey(prev), entryKey(cur))
							return
						}
						if cur.Kind != Insert {
							t.Errorf("快照只应包含 Insert 条目，got %s", cur.Kind)
							return
						}
					}
					_ = j.DecisionLog()
				}
			}
		}()
	}

	ops := []Op{
		lOp(Insert, "l1", "k", "v"),
		rOp(Insert, "r1", "k", "v"),
		rOp(Insert, "r2", "k", "v"),
		rOp(Delete, "r1", "k", "v"),
		lOp(Delete, "l1", "k", "v"),
		rOp(Delete, "r2", "k", "v"),
	}
	for _, op := range ops {
		if _, err := j.Apply([]Op{op}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	// 全部删空后视图应为空。
	if len(j.Snapshot()) != 0 {
		t.Fatalf("全部删除后快照应为空，got %+v", j.Snapshot())
	}
}

// TestDecisionLogContent 日志中必须打印输入、输出条目与判定依据，
// 拒绝批也要记录原因，但不进入结构化判定日志。
func TestDecisionLogContent(t *testing.T) {
	var buf bytes.Buffer
	j := New(0).WithLogWriter(&buf)

	res, err := j.Apply([]Op{lOp(Insert, "l1", "k", "v")})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("期望 1 条输出，got %d", len(res.Entries))
	}

	if _, err := j.Apply([]Op{rOp(Insert, "r1", "", "v")}); err == nil {
		t.Fatal("空键应被拒绝")
	}

	text := buf.String()
	for _, want := range []string{
		"op[0] ACCEPT",
		"left insert",
		`id="l1" key="k"`,
		"basis:",
		"out insert",
		"empty padded",
		"op[0] REJECT",
		"reason=empty_key",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("判定日志缺少 %q，实际输出:\n%s", want, text)
		}
	}

	log := j.DecisionLog()
	if len(log) != 1 || !log[0].Accepted || log[0].Detail == "" || len(log[0].Entries) != 1 {
		t.Fatalf("结构化判定日志只应包含 1 条已接受记录，got %+v", log)
	}
}

// TestDecisionLogCopy 保证外部无法通过返回值修改内部日志。
func TestDecisionLogCopy(t *testing.T) {
	j := New(0)
	if _, err := j.Apply([]Op{lOp(Insert, "l1", "k", "v")}); err != nil {
		t.Fatal(err)
	}
	log := j.DecisionLog()
	log[0].Detail = "tampered"
	if j.DecisionLog()[0].Detail == "tampered" {
		t.Fatal("DecisionLog 必须返回副本")
	}
}
