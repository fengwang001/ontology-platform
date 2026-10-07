package batchimport

import (
	"fmt"
	"sync"
	"testing"
)

func mustWidget(amount int64, label string) map[string]any {
	return map[string]any{"amount": amount, "label": label}
}

// TestVisibilityFollowsListOrder 钩子看到的是「之前且已通过校验」的前缀效果，
// 不是批次开始前快照或整批结束后的最终状态。
func TestVisibilityFollowsListOrder(t *testing.T) {
	spec := hookSpec{sumPre: true}

	t.Run("prefix_visible_future_invisible", func(t *testing.T) {
		r := NewRegistry()
		registerTestTypes(r, spec)
		records := []Record{
			{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")},
			{Type: typeWidget, ID: "w2", Fields: mustWidget(2, "b")},
			{Type: typeWidget, ID: "w3", Fields: mustWidget(4, "c")},
		}
		rep := r.Batch(records, AllOrNothing, 1)
		fmt.Printf("INPUT (prefix test):\n%sACTUAL:\n%s", formatRecords(records), formatReport(rep))
		if !rep.Committed || rep.Err != nil {
			t.Fatalf("依据: 严格前缀可见时 1,2,3 应全部通过, got err=%v", rep.Err)
		}
		if len(r.SnapshotType(typeWidget)) != 3 {
			t.Fatal("依据: 提交后应存在 3 条实例")
		}
	})

	t.Run("failed_record_excluded_from_later_visibility", func(t *testing.T) {
		r := NewRegistry()
		registerTestTypes(r, spec)
		records := []Record{
			{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")},
			{Type: typeWidget, ID: "w2", Fields: mustWidget(99, "b")},
			{Type: typeWidget, ID: "w3", Fields: mustWidget(2, "c")},
		}
		rep := r.Batch(records, BestEffort, 1)
		fmt.Printf("INPUT (exclusion test):\n%sACTUAL:\n%s", formatRecords(records), formatReport(rep))
		if !rep.Committed {
			t.Fatalf("尽力而为批次应提交部分成功, got %v", rep.Err)
		}
		if !rep.Records[0].OK || rep.Records[1].OK || !rep.Records[2].OK {
			t.Fatalf("依据: 失败记录不推进可见上界, w3 的 amount=2 必须通过; got %+v", rep.Records)
		}
		snap := r.SnapshotType(typeWidget)
		if _, ok := snap["w2"]; ok {
			t.Fatal("依据: w2 钩子失败后不得残留于最终状态")
		}
		if len(snap) != 2 {
			t.Fatalf("依据: 仅 w1/w3 生效, got %d 条", len(snap))
		}
	})
}

// TestSemanticsDifference 同一输入在两种语义下结果不同且各自自洽。
func TestSemanticsDifference(t *testing.T) {
	spec := hookSpec{sumPre: true}
	records := []Record{
		{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")},
		{Type: typeWidget, ID: "w2", Fields: mustWidget(5, "b")},
		{Type: typeWidget, ID: "w3", Fields: mustWidget(2, "c")},
	}

	rAll := NewRegistry()
	registerTestTypes(rAll, spec)
	repAll := rAll.Batch(records, AllOrNothing, 1)
	fmt.Printf("INPUT (semantics test):\n%sALL-OR-NOTHING:\n%s", formatRecords(records), formatReport(repAll))
	if repAll.Committed || repAll.Err == nil {
		t.Fatal("依据: 全有或全无语义下 w2 前置失败必须整批不生效")
	}
	if e, ok := AsImportError(repAll.Err); !ok || e.Kind != KindPreHook || e.Index != 1 {
		t.Fatalf("依据: 须归一化为前置钩子失败且定位下标 1, got %v", repAll.Err)
	}
	if len(rAll.SnapshotType(typeWidget)) != 0 {
		t.Fatal("依据: 撤销后状态须与批次开始前完全一致（0 条）")
	}

	rBest := NewRegistry()
	registerTestTypes(rBest, spec)
	repBest := rBest.Batch(records, BestEffort, 1)
	fmt.Printf("BEST-EFFORT:\n%s", formatReport(repBest))
	if !repBest.Committed || repBest.Err != nil {
		t.Fatal("依据: 尽力而为语义下批次整体提交、无批次级错误")
	}
	ok := [3]bool{repBest.Records[0].OK, repBest.Records[1].OK, repBest.Records[2].OK}
	if ok != [3]bool{true, false, true} {
		t.Fatalf("依据: 逐条清单须为 成功/失败/成功, got %+v", ok)
	}
	if len(rBest.SnapshotType(typeWidget)) != 2 {
		t.Fatal("依据: w1/w3 生效、w2 跳过, 最终应有 2 条")
	}
}

func reportsEqual(a, b *Report) bool {
	if a.Committed != b.Committed || len(a.Records) != len(b.Records) {
		return false
	}
	for i := range a.Records {
		x, y := a.Records[i], b.Records[i]
		if x.OK != y.OK || x.Reason != y.Reason {
			return false
		}
	}
	if (a.Err == nil) != (b.Err == nil) {
		return false
	}
	if a.Err != nil && a.Err.Error() != b.Err.Error() {
		return false
	}
	return true
}

func snapsEqual(a, b map[string]Instance) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va.ID != vb.ID || len(va.Fields) != len(vb.Fields) {
			return false
		}
		for f, v := range va.Fields {
			if vb.Fields[f] != v {
				return false
			}
		}
	}
	return true
}

var _ = sync.WaitGroup{}
