package policy

import "testing"

// 六种存活现状（含不可由公开 API 直接观测的 Terminated 存活态）外加 nil（不存在/已过期）。
func tableRecords() map[string]*Record {
	states := map[string]Status{
		"Running": Running, "Completed": Completed, "Failed": Failed,
		"Cancelled": Cancelled, "Terminated": Terminated,
	}
	out := map[string]*Record{}
	for name, st := range states {
		out[name] = &Record{Run: 1, Owner: []byte("o"), State: st, Ended: 10}
	}
	out["Missing"] = nil
	out["Expired"] = nil
	return out
}

// wantTable 是按题目步骤顺序推导出的期望结论：
// Running 只看 conflict；已结束看 reuse（Completed+AllowFailedOnly 与 Reject 拒绝）；nil 一律新建。
func wantTable(rec *Record, reuse Reuse, conflict Conflict) Action {
	if rec == nil {
		return ActionCreate
	}
	if rec.State == Running {
		switch conflict {
		case Fail:
			return ActionRejectRunning
		case UseExisting:
			return ActionReturnExisting
		case Terminate:
			return ActionTerminateAndCreate
		}
	}
	switch reuse {
	case AllowAll:
		return ActionReuse
	case AllowFailedOnly:
		if rec.State == Failed || rec.State == Cancelled || rec.State == Terminated {
			return ActionReuse
		}
	}
	return ActionDenyReuse
}

// TestDecideMatrix 覆盖 reuse 三种 × conflict 三种 = 九种组合 × 七种现状 = 63 格。
func TestDecideMatrix(t *testing.T) {
	recs := tableRecords()
	names := []string{"Running", "Completed", "Failed", "Cancelled", "Terminated", "Expired", "Missing"}
	total := 0
	for _, name := range names {
		rec := recs[name]
		for reuse := AllowAll; reuse <= Reject; reuse++ {
			for conflict := Fail; conflict <= Terminate; conflict++ {
				got := Decide(rec, reuse, conflict)
				want := wantTable(rec, reuse, conflict)
				total++
				t.Logf("现状=%s reuse=%d conflict=%d -> 动作=%d 期望=%d 依据: Running看conflict/已结束看reuse/nil新建",
					name, reuse, conflict, got, want)
				if got != want {
					t.Fatalf("%s reuse=%d conflict=%d = %d want %d", name, reuse, conflict, got, want)
				}
			}
		}
	}
	if total != 63 {
		t.Fatalf("cells=%d want 63", total)
	}
}

// TestRunningIgnoresReuse Terminate/UseExisting/Fail 三条 Running 路径均不受 reuse 影响。
func TestRunningIgnoresReuse(t *testing.T) {
	rec := &Record{Run: 7, State: Running}
	for reuse := AllowAll; reuse <= Reject; reuse++ {
		if got := Decide(rec, reuse, UseExisting); got != ActionReturnExisting {
			t.Fatalf("reuse=%d UseExisting = %d", reuse, got)
		}
		if got := Decide(rec, reuse, Terminate); got != ActionTerminateAndCreate {
			t.Fatalf("reuse=%d Terminate = %d", reuse, got)
		}
	}
}
