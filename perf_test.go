package ontology

import (
	"fmt"
	"testing"
)

// buildSizedPair 构造一对快照：n 个对象、n/10 条链接，
// 第二份快照上施加固定数量 k 的真实变更。
func buildSizedPair(n int) (*Snapshot, *Snapshot) {
	b0 := NewBuilder("L", 1)
	b0.AddObjectType("item", "Item",
		Property{ID: "pk", Key: "id", Type: TypeInt},
		Property{ID: "p-val", Key: "val", Type: TypeString},
	)
	b0.AddObjectType("tag", "Tag", Property{ID: "pk", Key: "id", Type: TypeInt})
	b0.AddLinkType("tagged", "Tagged", "item", "tag")
	for i := 0; i < 10; i++ {
		b0.PutObject("tag", IntValue(int64(i)), nil)
	}
	for i := 0; i < n; i++ {
		b0.PutObject("item", IntValue(int64(i)), map[string]Value{
			"p-val": StringValue(fmt.Sprintf("v%d", i)),
		})
		if i%10 == 0 {
			b0.PutLink("tagged",
				ObjectRef{TypeID: "item", PK: IntValue(int64(i))},
				ObjectRef{TypeID: "tag", PK: IntValue(int64(i % 10))})
		}
	}
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	// 固定 k 个真实变更，与 n 无关。
	for i := 0; i < 5; i++ { // 5 个更新
		b1.PutObject("item", IntValue(int64(i*2)), map[string]Value{
			"p-val": StringValue("changed"),
		})
	}
	for i := 0; i < 3; i++ { // 3 个创建
		b1.PutObject("item", IntValue(int64(1_000_000+i)), map[string]Value{
			"p-val": StringValue("new"),
		})
	}
	b1.DeleteObject("item", IntValue(500)) // 2 个删除
	b1.DeleteObject("item", IntValue(501))
	b1.PutLink("tagged", // 1 个链接创建
		ObjectRef{TypeID: "item", PK: IntValue(1)},
		ObjectRef{TypeID: "tag", PK: IntValue(1)})
	b1.DeleteLink("tagged", // 1 个链接显式删除
		ObjectRef{TypeID: "item", PK: IntValue(20)},
		ObjectRef{TypeID: "tag", PK: IntValue(0)})
	s1 := b1.Build()
	return s0, s1
}

// 可复核证明：同一谱系下，比对工作量（Stats.Inspected）不随
// 未变化对象与链接总数增长，只与真实差异数相关。
func TestWorkIsIndependentOfUnchangedCount(t *testing.T) {
	sizes := []int{1_000, 10_000, 100_000}
	var prevStats *Stats
	var prevDiff string
	for _, n := range sizes {
		s0, s1 := buildSizedPair(n)
		res := mustCompare(t, s0, s1)
		if res.Stats.FullScan {
			t.Fatalf("n=%d: expected fast path", n)
		}
		diffJSON := canonicalJSON(t, res.Instances)
		if prevStats != nil {
			if res.Stats != *prevStats {
				t.Fatalf("n=%d: stats grew with unchanged count: prev=%+v got=%+v",
					n, prevStats, res.Stats)
			}
			if diffJSON != prevDiff {
				t.Fatalf("n=%d: instance diff changed with size", n)
			}
		}
		s := res.Stats
		prevStats = &s
		prevDiff = diffJSON
		t.Logf("n=%d inspected=%d (candidates=%d tombstones=%d lookups=%d)",
			n, res.Stats.Inspected(), res.Stats.Candidates, res.Stats.Tombstones, res.Stats.BaseLookups)
	}
}
