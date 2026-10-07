package restore

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func verdictsEqual(a, b *Verdict) bool {
	if len(a.Records) != len(b.Records) || len(a.Plan) != len(b.Plan) || len(a.Errors) != len(b.Errors) {
		return false
	}
	for k, ra := range a.Records {
		rb, ok := b.Records[k]
		if !ok || ra.Recoverable != rb.Recoverable ||
			!reflect.DeepEqual(ra.Reasons, rb.Reasons) {
			return false
		}
	}
	if !reflect.DeepEqual(planSignatures(a.Plan), planSignatures(b.Plan)) {
		return false
	}
	if !reflect.DeepEqual(a.ClassStatuses, b.ClassStatuses) {
		return false
	}
	for i := range a.Errors {
		if a.Errors[i].Code != b.Errors[i].Code {
			return false
		}
	}
	return true
}

func planSignatures(p []PlanStep) []string {
	out := make([]string, len(p))
	for i, st := range p {
		if st.Kind == StepBarrier {
			out[i] = "barrier:" + st.CompletedClass.String()
		} else {
			out[i] = st.Record.String()
		}
	}
	return out
}

// TestConcurrentAdjudication 并发裁决同一批数据，所有结论必须完全一致，
// 且裁决不修改输入（通过裁决后输入仍与原快照等价来侧面保证只读）。
func TestConcurrentAdjudication(t *testing.T) {
	snap := healthySnapshot()
	for i := range snap.Objects {
		if snap.Objects[i].Key == "o2" {
			snap.Objects[i].State = StateCorrupt
		}
	}
	const n = 32
	results := make([]*Verdict, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = New().Adjudicate(snap)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !verdictsEqual(results[0], results[i]) {
			t.Fatalf("concurrent verdict %d differs from verdict 0", i)
		}
	}
}

// chainSnapshot 构造长度为 n 的对象依赖链（通过 ExtraDeps）：
// o0 <- o1 <- ... <- o(n-1)，每个对象都属于完好类型 tc。
func chainSnapshot(n int) *Snapshot {
	snap := &Snapshot{
		Classes: map[Class]ClassBackup{
			ClassType: {}, ClassObject: {}, ClassLink: {}, ClassAction: {},
		},
	}
	for i := 0; i < n; i++ {
		key := chainKey(i)
		// 不挂类型键，使依赖边仅来自 ExtraDeps 构造的链，边数即链长。
		snap.Objects = append(snap.Objects, ObjectInstance{Key: key, State: StateIntact})
	}
	for i := 1; i < n; i++ {
		snap.ExtraDeps = append(snap.ExtraDeps, Edge{
			From: id(ClassObject, chainKey(i)),
			To:   id(ClassObject, chainKey(i-1)),
		})
	}
	return snap
}

func chainKey(i int) string {
	return "o" + padIndex(i)
}

func padIndex(i int) string {
	return fmt.Sprintf("%05d", i)
}

// TestPerRecordWorkBoundedByChainLength 复核单条记录的判定增量开销
// 只与其自身依赖链长度相关：在同一记忆化会话内，对链尾的查询查看边数
// 等于链长；再对独立分支查询，其查看边数等于该分支链长，与总规模无关。
func TestPerRecordWorkBoundedByChainLength(t *testing.T) {
	const n = 40
	snap := chainSnapshot(n)
	sess := New().NewCheckSession(snap)
	tail := id(ClassObject, chainKey(n-1))
	got := sess.Check(tail)
	if !got.Recoverable {
		t.Fatalf("chain tail should recover")
	}
	if got.EdgesLooked != n-1 {
		t.Fatalf("tail edges looked = %d, want %d", got.EdgesLooked, n-1)
	}
	// 再查一个“旁挂”短链：新增对象 z0<-z1，长度1。
	// 其查询查看边数必须是 1，而不是随主链规模 n 增长。
	snap.Objects = append(snap.Objects,
		ObjectInstance{Key: "z0", State: StateIntact},
		ObjectInstance{Key: "z1", State: StateIntact},
	)
	snap.ExtraDeps = append(snap.ExtraDeps, Edge{
		From: id(ClassObject, "z1"), To: id(ClassObject, "z0"),
	})
	// 在新会话上复核短链，保证它不享受主链缓存，纯粹由自身长度决定。
	z := New().NewCheckSession(snap).Check(id(ClassObject, "z1"))
	if z.EdgesLooked != 1 {
		t.Fatalf("side-chain edges looked = %d, want 1 (must not scale with n=%d)", z.EdgesLooked, n)
	}
}
