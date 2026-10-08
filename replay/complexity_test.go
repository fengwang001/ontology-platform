package replay

import (
	"fmt"
	"testing"
)

// scaledSnapshot 在基础快照之外加入 n 个未被差异记录触及的对象与链接。
func scaledSnapshot(n int) *Snapshot {
	snap := baseSnapshot()
	snap.Schema.ObjectTypes["Doc"] = objType("Doc", map[string]string{"title": PropString})
	snap.Schema.LinkTypes["Ref"] = LinkType{Name: "Ref", SourceType: "Doc", TargetType: "Doc"}
	prev := ""
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("u%d", i)
		snap.Objects[id] = obj(id, "Doc", map[string]any{"title": id})
		if prev != "" {
			lid := fmt.Sprintf("ul%d", i)
			snap.Links[lid] = lnk(lid, "Ref", prev, id)
		}
		prev = id
	}
	return NewSnapshot(snap.Schema, snap.Objects, snap.Links)
}

// TestCostIndependentOfUntouched 复核“校验开销只与差异记录规模相关”：
// 快照中未被触及的对象与链接数量从 0 增长到 100000，
// 校验对快照的读取次数（Stats）必须保持完全一致。
func TestCostIndependentOfUntouched(t *testing.T) {
	delta := goodDelta()
	var want Stats
	for i, n := range []int{0, 10, 1000, 100000} {
		snap := scaledSnapshot(n)
		v := NewValidator().Validate(snap, delta)
		expectVerdict(t, v, Valid, NoLocation)
		if i == 0 {
			want = v.Stats
			continue
		}
		if v.Stats != want {
			t.Fatalf("未触及规模 %d 时快照读取次数 %v，与基准 %v 不一致", n, v.Stats, want)
		}
	}
}

// BenchmarkValidateScaling 基准：同一差异记录在不同未触及规模的快照上校验，
// 耗时应近似恒定（与 TestCostIndependentOfUntouched 的计数复核互为印证）。
func BenchmarkValidateScaling(b *testing.B) {
	delta := goodDelta()
	for _, n := range []int{100, 10000, 1000000} {
		snap := scaledSnapshot(n)
		validator := NewValidator()
		b.Run(fmt.Sprintf("untouched=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				validator.Validate(snap, delta)
			}
		})
	}
}
