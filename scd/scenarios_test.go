package scd

import (
	"testing"
)

// TestSequentialOpenAndClose 顺序新开区间与删除闭合。
func TestSequentialOpenAndClose(t *testing.T) {
	h := NewHistory()
	log := newStepLogger(t)
	const key = "dim.city"

	log.commit(h, []Event{{Key: key, At: 10, Op: OpUpdate, Value: "BJ"}},
		"顺序新开：第一个更新点生成到 MaxTime 的开放区间")
	log.dump(h, []string{key}, []int64{9, 10, 15})
	assertIntervals(t, h, key, []Interval{iv(key, 10, MaxTime, "BJ")})

	log.commit(h, []Event{{Key: key, At: 20, Op: OpUpdate, Value: "SH"}},
		"顺序到达：新更新点把前行闭合到 20，并开新行")
	log.dump(h, []string{key}, []int64{19, 20, 21})
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "BJ"),
		iv(key, 20, MaxTime, "SH"),
	})

	log.commit(h, []Event{{Key: key, At: 30, Op: OpDelete}},
		"删除点：闭合上一更新区间且自身不产生区间")
	log.dump(h, []string{key}, []int64{29, 30, 100})
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "BJ"),
		iv(key, 20, 30, "SH"),
	})

	log.commit(h, []Event{{Key: key, At: 40, Op: OpUpdate, Value: "GZ"}},
		"删除之后再次更新：删除点到 40 之间保持无区间")
	log.dump(h, []string{key}, []int64{35, 40})
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "BJ"),
		iv(key, 20, 30, "SH"),
		iv(key, 40, MaxTime, "GZ"),
	})
}

// TestOutOfOrderSplit 乱序事件落在已有区间内部时拆分该行。
func TestOutOfOrderSplit(t *testing.T) {
	h := NewHistory()
	log := newStepLogger(t)
	const key = "dim.tier"

	log.commit(h, []Event{
		{Key: key, At: 10, Op: OpUpdate, Value: "A"},
		{Key: key, At: 40, Op: OpUpdate, Value: "D"},
	}, "先建立 [10,40)=A 与 [40,Max)=D")
	log.dump(h, []string{key}, nil)

	log.commit(h, []Event{{Key: key, At: 20, Op: OpUpdate, Value: "B"}},
		"乱序事件 at=20 落在 [10,40) 内部：拆分为 [10,20) 与 [20,40)")
	log.dump(h, []string{key}, []int64{19, 20, 39})
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "A"),
		iv(key, 20, 40, "B"),
		iv(key, 40, MaxTime, "D"),
	})

	log.commit(h, []Event{{Key: key, At: 30, Op: OpDelete}},
		"乱序删除点 at=30 落在 [20,40) 内部：只产生闭合，30→40 无值")
	log.dump(h, []string{key}, []int64{25, 30, 39})
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "A"),
		iv(key, 20, 30, "B"),
		iv(key, 40, MaxTime, "D"),
	})
}

// TestReplaceSameEffectiveTime 恰等于已有变更点时以后到者替换该点。
func TestReplaceSameEffectiveTime(t *testing.T) {
	h := NewHistory()
	log := newStepLogger(t)
	const key = "dim.owner"

	log.commit(h, []Event{
		{Key: key, At: 10, Op: OpUpdate, Value: "v1"},
		{Key: key, At: 20, Op: OpUpdate, Value: "v2"},
	}, "建立两个变更点")
	log.dump(h, []string{key}, nil)

	beforeCount := h.ChangePointCount(key)
	log.commit(h, []Event{{Key: key, At: 10, Op: OpUpdate, Value: "v1-fix"}},
		"后到事件 at=10 与已有变更点相等：替换取值，不新增变更点")
	log.dump(h, []string{key}, []int64{10, 19})
	if h.ChangePointCount(key) != beforeCount {
		t.Fatalf("替换后变更点数变化: before=%d after=%d", beforeCount, h.ChangePointCount(key))
	}
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "v1-fix"),
		iv(key, 20, MaxTime, "v2"),
	})

	log.commit(h, []Event{{Key: key, At: 20, Op: OpDelete}},
		"后到删除事件 at=20：把更新点替换为删除点，末区间消失")
	log.dump(h, []string{key}, []int64{20, 50})
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "v1-fix"),
	})

	log.commit(h, []Event{{Key: key, At: 20, Op: OpUpdate, Value: "v2-resurrect"}},
		"再次后到更新 at=20：删除点被替换回更新点，区间重新出现")
	log.dump(h, []string{key}, nil)
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "v1-fix"),
		iv(key, 20, MaxTime, "v2-resurrect"),
	})
}

// TestInvalidBatchRejectedAndNoTrace 非法输入整批拒绝、错误码互不相同、拒绝后状态不变。
func TestInvalidBatchRejectedAndNoTrace(t *testing.T) {
	h := NewHistory(WithMaxPointsPerKey(3))
	log := newStepLogger(t)
	const key = "dim.bad"

	log.commit(h, []Event{{Key: key, At: 10, Op: OpUpdate, Value: "base"}}, "写入合法基线")
	baseline := h.Intervals(key)
	baselineCount := h.ChangePointCount(key)
	log.dump(h, []string{key}, nil)

	bad := []Event{
		{Key: "", At: 10, Op: OpUpdate, Value: "x"},           // 空键
		{Key: key, At: 10, Op: OpUnknown, Value: "x"},         // 非法操作
		{Key: key, At: MaxTime + 1, Op: OpUpdate, Value: "x"}, // 时间越界（上）
		{Key: key, At: MinTime - 1, Op: OpDelete},             // 时间越界（下）
	}
	err := log.commit(h, bad, "一批含 4 类静态非法输入：必须整批拒绝且给出全部原因")
	if err == nil {
		t.Fatalf("期望整批拒绝，实际成功")
	}
	ce := err.(*CommitError)
	seen := map[ErrorCode]int{}
	for _, r := range ce.Reasons {
		seen[r.Code]++
		t.Logf("  拒绝原因: %s", r.String())
	}
	for _, c := range []ErrorCode{CodeEmptyKey, CodeInvalidOp, CodeTimeOutOfRange} {
		if seen[c] == 0 {
			t.Fatalf("缺少错误类别 %s，实际原因: %v", c, ce.Reasons)
		}
	}
	if seen[CodeTimeOutOfRange] != 2 {
		t.Fatalf("越界原因应出现 2 次，实际 %d 次", seen[CodeTimeOutOfRange])
	}
	if !intervalsEqual(h.Intervals(key), baseline) || h.ChangePointCount(key) != baselineCount {
		t.Fatalf("拒绝后状态被改变: intervals=%v count=%d", h.Intervals(key), h.ChangePointCount(key))
	}
	t.Logf("  判定依据: 拒绝后区间与基线完全一致 → 失败不留痕")

	err = log.commit(h, nil, "空批次：独立错误类别")
	if ce, ok := err.(*CommitError); !ok || !ce.HasCode(CodeEmptyBatch) {
		t.Fatalf("空批应返回 %s，实际 %v", CodeEmptyBatch, err)
	}
	if len(h.Keys()) != 1 {
		t.Fatalf("空批拒绝后不应留下任何键痕迹")
	}

	over := []Event{
		{Key: key, At: 20, Op: OpUpdate, Value: "c2"},
		{Key: key, At: 30, Op: OpUpdate, Value: "c3"},
		{Key: key, At: 40, Op: OpUpdate, Value: "c4"},
	}
	err = log.commit(h, over, "超过每键变更点上限 3：整批拒绝")
	if ce, ok := err.(*CommitError); !ok || !ce.HasCode(CodeTooManyChanges) {
		t.Fatalf("应返回 %s，实际 %v", CodeTooManyChanges, err)
	}
	if !intervalsEqual(h.Intervals(key), baseline) || h.ChangePointCount(key) != baselineCount {
		t.Fatalf("超限拒绝后状态被改变")
	}

	log.commit(h, []Event{
		{Key: key, At: 20, Op: OpUpdate, Value: "c2"},
		{Key: key, At: 30, Op: OpUpdate, Value: "c3"},
	}, "补齐到上限 3 个变更点")
	log.commit(h, []Event{{Key: key, At: 20, Op: OpUpdate, Value: "c2b"}},
		"上限已满但仅替换已有变更点：允许提交")
	log.dump(h, []string{key}, nil)
	if h.ChangePointCount(key) != 3 {
		t.Fatalf("替换不应改变点数，实际 %d", h.ChangePointCount(key))
	}
}

// TestAdjacentSameValueNotMerged 相邻同值区间不得合并。
func TestAdjacentSameValueNotMerged(t *testing.T) {
	h := NewHistory()
	log := newStepLogger(t)
	const key = "dim.flag"

	log.commit(h, []Event{
		{Key: key, At: 10, Op: OpUpdate, Value: "X"},
		{Key: key, At: 20, Op: OpUpdate, Value: "X"},
		{Key: key, At: 30, Op: OpUpdate, Value: "X"},
	}, "连续三个更新点取值相同：区间仍为三行，不合并")
	log.dump(h, []string{key}, nil)
	assertIntervals(t, h, key, []Interval{
		iv(key, 10, 20, "X"),
		iv(key, 20, 30, "X"),
		iv(key, 30, MaxTime, "X"),
	})
}
