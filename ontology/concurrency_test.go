package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSerializable 并发触发涉及重叠对象类型与链接关系的动作，
// 最终状态必须等价于某个串行顺序：用审计记录逐项重算每个实例的
// 版本号、时钟与实例集合，与提交后的实际状态逐一核对。
func TestConcurrentSerializable(t *testing.T) {
	s := baseStore()
	for i := 0; i < 20; i++ {
		putA(s, InstanceID(fmt.Sprintf("a%d", i)))
	}
	// 重叠的链接关系：相邻实例互连。
	for i := 0; i < 19; i++ {
		s.AddLink(Link{Type: "aa", From: InstanceID(fmt.Sprintf("a%d", i)), To: InstanceID(fmt.Sprintf("a%d", i+1))})
	}
	eng := NewEngine(s, allowAll())
	eng.RegisterAction(ActionDecl{
		ID:         "touch",
		AllowedOps: map[ObjectTypeID][]OpKind{"A": {OpModify}},
		Cascade:    CascadeRule{MaxDepth: 1},
		Invisible:  InvisibleSkip,
		Merge:      MergeAll,
	})
	eng.RegisterAction(ActionDecl{
		ID:         "bad",
		AllowedOps: map[ObjectTypeID][]OpKind{"A": {OpModify}},
		Cascade:    CascadeRule{MaxDepth: 0}, // 有出边，必然深度超界被拒绝
		Invisible:  InvisibleDeny,
		Merge:      MergeAll,
	})
	const workers = 16
	const perWorker = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				target := InstanceID(fmt.Sprintf("a%d", (w+i)%20))
				action := ActionTypeID("touch")
				if i%5 == 4 {
					action = "bad" // 周期性注入必然被拒绝的动作
				}
				eng.Execute(SubjectID(fmt.Sprintf("s%d", w)), Invocation{
					Action: action,
					Ops:    []DirectOp{modifyOp(target)},
				})
			}
		}(w)
	}
	wg.Wait()
	// 判定日志必须覆盖全部调用。
	if got := len(eng.Log()); got != workers*perWorker {
		t.Fatalf("decision log has %d records, want %d", got, workers*perWorker)
	}
	// 时钟 == 已提交动作数；被拒绝的动作不推进时钟。
	if s.Clock != int64(len(s.Audit)) {
		t.Fatalf("clock=%d != committed actions=%d", s.Clock, len(s.Audit))
	}
	// 用审计记录重算版本号：等价于按提交顺序串行执行。
	wantVersion := map[InstanceID]int64{}
	for i := 0; i < 20; i++ {
		wantVersion[InstanceID(fmt.Sprintf("a%d", i))] = 1
	}
	for _, rec := range s.Audit {
		for _, id := range rec.Modified {
			wantVersion[id]++
		}
		for _, id := range rec.Touched {
			wantVersion[id]++
		}
	}
	snap := s.Snapshot()
	for id, want := range wantVersion {
		if got := snap.Instances[id].Version; got != want {
			t.Errorf("instance %s version=%d, want %d (serial replay of audit log)", id, got, want)
		}
	}
	// 被拒绝的动作（bad）不得在任何审计记录中出现。
	for _, rec := range s.Audit {
		if rec.Action == "bad" {
			t.Fatalf("rejected action leaked into audit log: %+v", rec)
		}
	}
}
