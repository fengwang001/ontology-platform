package ontology

import "testing"

// TestScanBoundedByLiveCount 用可验证的方式证明：兼容性判定所扫描的
// 实例数量只取决于当前存活实例数，与历史上写入、逻辑删除、迁移走的
// 实例总量无关。
func TestScanBoundedByLiveCount(t *testing.T) {
	store := NewInstanceStore()

	// 制造大量“历史实例”：写入后全部逻辑删除或迁移走。
	const historical = 500
	for i := 0; i < historical; i++ {
		id := InstanceID("h" + itoa(i))
		store.Put(id, map[string]Value{"n": NewValue(int64(i))})
		if i%2 == 0 {
			store.Tombstone(id)
		} else {
			store.MigrateOut(id)
		}
	}
	// 再写入一小批当前存活实例。
	const liveN = 7
	for i := 0; i < liveN; i++ {
		store.Put(InstanceID("l"+itoa(i)), map[string]Value{"n": NewValue(int64(i))})
	}

	if got := store.LiveCount(); got != liveN {
		t.Fatalf("LiveCount=%d, want %d", got, liveN)
	}

	store.ResetScanCounter()
	seen := 0
	store.Scan(func(Instance) bool {
		seen++
		return true
	})
	scanned := store.ScannedSinceReset()

	if seen != liveN || scanned != liveN {
		t.Fatalf("scan visited %d instances (counter=%d), want exactly %d live; "+
			"historical total was %d", seen, scanned, liveN, historical)
	}
	if int64(scanned) > int64(store.LiveCount()) {
		t.Fatalf("scanned %d exceeds live count %d", scanned, store.LiveCount())
	}

	// 同样的收紧判定：只有 7 个存活实例可能被检查，历史 500 个零开销。
	store.ResetScanCounter()
	c := NewChecker("O", store, nil, nil)
	rep := c.CheckBatch([]FieldChange{{Name: "n",
		Old: &FieldDef{Type: NewIntType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue(int64(0))},
		New: &FieldDef{Type: NewIntType(), Constraint: NewRangeConstraint(0, 2, true, true),
			HasDefault: true, Default: NewValue(int64(0))}}})
	if store.ScannedSinceReset() != int64(liveN) {
		t.Fatalf("check scanned %d instances, want exactly %d live",
			store.ScannedSinceReset(), liveN)
	}
	// 存活值 0..6 中 3..6 违规 => 不兼容；历史实例无论取何值都不影响结论。
	if rep.Compatible {
		t.Fatal("expected rejection from live violating values")
	}
	if len(rep.Items[0].Bases[0].Instances) != 4 {
		t.Fatalf("violators=%v", rep.Items[0].Bases[0].Instances)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
