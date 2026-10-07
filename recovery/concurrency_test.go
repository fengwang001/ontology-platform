package recovery

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentAppendAndRepairIsSerializable 高并发追加（含损坏帧）与
// 修复/查询：最终可观察结果须等价于某个全局串行顺序。配合 -race 检测
// 数据竞争；原子动作保证每次观察中 a、b 始终处于同一“代”。
func TestConcurrentAppendAndRepairIsSerializable(t *testing.T) {
	base := Version{Epoch: 7, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "gen#0"},
		{ID: "b", Exists: true, State: "gen#0"},
	}}
	c, err := NewCoordinator(mustEncodeSnapshot(t, snap),
		mustEncodeLog(t, base, nil), NopLogger{})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	const writers, perWriter = 8, 60
	var seqMu sync.Mutex
	nextSeq := 0

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < perWriter; k++ {
				seqMu.Lock()
				nextSeq++
				seq := nextSeq
				seqMu.Unlock()
				a := Action{
					Seq:     seq,
					Version: Version{Epoch: 7, Seq: int64(seq)},
					Effects: map[ObjectID]ObjectEffect{
						"a": {Change: State(fmt.Sprintf("gen#%d", seq))},
						"b": {Change: State(fmt.Sprintf("gen#%d", seq))},
					},
				}
				rec, _ := EncodeAction(a)
				if w == 0 && k%17 == 0 {
					rec = flipByte(t, rec, len(rec)/2) // 4 个损坏帧
				}
				if err := c.Append(rec); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				rep, err := c.RepairAll()
				if err != nil {
					t.Error(err)
					return
				}
				ga := strings.TrimPrefix(string(rep.Objects["a"].State), "gen#")
				gb := strings.TrimPrefix(string(rep.Objects["b"].State), "gen#")
				if ga != gb {
					t.Errorf("non-serializable intermediate state: a=%s b=%s", ga, gb)
					return
				}
			}
		}()
	}
	wg.Wait()

	rep, _ := c.RepairAll()
	applied, corrupt := 0, 0
	for _, j := range rep.Judgments {
		switch j.Outcome {
		case ActionApplied:
			applied++
		case ActionCorrupt:
			corrupt++
		}
	}
	if corrupt != 4 {
		t.Fatalf("corrupt count = %d want 4", corrupt)
	}
	if applied != writers*perWriter-corrupt {
		t.Fatalf("applied = %d want %d", applied, writers*perWriter-corrupt)
	}
	ga := strings.TrimPrefix(string(rep.Objects["a"].State), "gen#")
	gb := strings.TrimPrefix(string(rep.Objects["b"].State), "gen#")
	if ga != gb {
		t.Fatalf("final state divergence: a=%s b=%s", ga, gb)
	}
}

// TestDecisionLoggerRecordsInputOutputReason 判定日志必须打印每次判定的
// 输入、输出与依据。
func TestDecisionLoggerRecordsInputOutputReason(t *testing.T) {
	var buf bytes.Buffer
	logger := NewTextLogger(&buf)
	base := Version{Epoch: 1, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "a", Exists: true, State: "A0"},
		{ID: "b", Exists: true, State: "B0"},
	}}
	rawSnap := mustEncodeSnapshot(t, snap)
	bHeader := findSubbyte(t, rawSnap, `{"id":"b"}`, 0)
	brokenSnap := flipByte(t, rawSnap, bHeader+len(`{"id":"b"}`)+8)
	a1 := Action{Seq: 1, Version: base.Next(), Effects: map[ObjectID]ObjectEffect{
		"a": {Change: "A1"},
		"b": {Change: "B1"},
	}}
	c, err := NewCoordinator(brokenSnap, mustEncodeLog(t, base, []Action{a1}), logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Repair([]ObjectID{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"snapshot_unreadable", "snapshot_ok", "connected",
		"blocked", "input :", "output:", "reason:",
		"record_index=0", "atomic precondition failed",
		"no applied action ever provided a start state",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("decision log missing %q\nfull log:\n%s", want, out)
		}
	}
}
