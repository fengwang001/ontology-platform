package ontology

import (
	"errors"
	"testing"
)

// snapshot 捕获存储的可观察状态，用于验证失败操作无副作用。
func snapshot(t *testing.T, s *Store, objectID string) (ExpandResult, []SchemaVersion) {
	t.Helper()
	res, err := s.Expand(ExpandRequest{
		ObjectID: objectID, AsOfRecord: RecordTime(1 << 60), ValidFrom: -1 << 60, ValidTo: 1 << 60,
	})
	if err != nil {
		t.Fatalf("snapshot expand: %v", err)
	}
	ts := s.types[s.objects[objectID].typeID]
	vs := make([]SchemaVersion, len(ts.versions))
	copy(vs, ts.versions)
	return res, vs
}

func equalExpansion(a, b ExpandResult) bool {
	if len(a.Facts) != len(b.Facts) {
		return false
	}
	for i := range a.Facts {
		fa, fb := a.Facts[i], b.Facts[i]
		if fa.ValidTime != fb.ValidTime || fa.RecordTime != fb.RecordTime ||
			fa.SchemaVersionID != fb.SchemaVersionID {
			return false
		}
		if len(fa.Props) != len(fb.Props) {
			return false
		}
		for k, pa := range fa.Props {
			pb, ok := fb.Props[k]
			if !ok || pa.Status != pb.Status || !pa.Value.Equal(pb.Value) {
				return false
			}
		}
	}
	return true
}

func setupAtomicityStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 0)
	mustRegister(t, s, "T", "o1")
	for i := 0; i < 5; i++ {
		mustWrite(t, s, Fact{
			ObjectID: "o1", ValidTime: ValidTime(i), RecordTime: RecordTime(100 + i),
			Values: map[string]Value{"a": IntValue(int64(i))},
		})
	}
	return s
}

// TestMigrationAtomicityUnderFaultInjection 在两个故障注入点分别注入
// 失败，验证迁移前后可观察状态完全一致（原子性）。
func TestMigrationAtomicityUnderFaultInjection(t *testing.T) {
	for _, fp := range []Failpoint{FailAfterValidation, FailBeforeCommit} {
		s := setupAtomicityStore(t)
		before, versionsBefore := snapshot(t, s, "o1")

		s.SetFailpoint(fp)
		err := s.Migrate(Migration{
			TypeID: "T", EffectiveFrom: 200,
			NewProps: map[string]PropertyDef{
				"a": {Name: "a", Type: TypeFloat, Required: true},
			},
		})
		if !errors.Is(err, ErrInjectedFault) {
			t.Fatalf("fp=%d: err = %v, want injected fault", fp, err)
		}
		s.SetFailpoint(FailNone)

		after, versionsAfter := snapshot(t, s, "o1")
		if len(versionsAfter) != len(versionsBefore) {
			t.Fatalf("fp=%d: version count changed %d -> %d", fp, len(versionsBefore), len(versionsAfter))
		}
		for i := range versionsBefore {
			if versionsAfter[i].ID != versionsBefore[i].ID ||
				versionsAfter[i].From != versionsBefore[i].From ||
				versionsAfter[i].To != versionsBefore[i].To {
				t.Fatalf("fp=%d: version %d mutated: %+v -> %+v", fp, i, versionsBefore[i], versionsAfter[i])
			}
		}
		if !equalExpansion(before, after) {
			t.Fatalf("fp=%d: expansion changed after failed migration", fp)
		}

		// 故障清除后同一迁移可以干净提交，且立即可见。
		if err := s.Migrate(Migration{
			TypeID: "T", EffectiveFrom: 200,
			NewProps: map[string]PropertyDef{
				"a": {Name: "a", Type: TypeFloat, Required: true},
			},
		}); err != nil {
			t.Fatalf("fp=%d: retry migrate: %v", fp, err)
		}
		if len(s.types["T"].versions) != len(versionsBefore)+1 {
			t.Fatalf("fp=%d: retry did not commit exactly one new version", fp)
		}
	}
}

// TestMigrationValidationFailureIsAtomic 校验失败时状态完全不变。
func TestMigrationValidationFailureIsAtomic(t *testing.T) {
	s := setupAtomicityStore(t)
	before, versionsBefore := snapshot(t, s, "o1")

	// 新增必填属性 r：既有事实（record >= 0）均缺少 r → 校验失败。
	err := s.Migrate(Migration{
		TypeID: "T", EffectiveFrom: 0,
		NewProps: map[string]PropertyDef{
			"a": {Name: "a", Type: TypeInt, Required: true},
			"r": {Name: "r", Type: TypeInt, Required: true},
		},
	})
	assertErrCode(t, err, ErrCodeMigrationValidation)

	after, versionsAfter := snapshot(t, s, "o1")
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatal("version count changed after validation failure")
	}
	if !equalExpansion(before, after) {
		t.Fatal("expansion changed after validation failure")
	}
}

// TestMigrationCleanSwitchVisibility 成功迁移对所有后续写入与
// 历史展开立即可见，且不存在中间状态。
func TestMigrationCleanSwitchVisibility(t *testing.T) {
	s := setupAtomicityStore(t)
	if err := s.Migrate(Migration{
		TypeID: "T", EffectiveFrom: 200,
		NewProps: map[string]PropertyDef{
			"a": {Name: "a", Type: TypeFloat, Required: true},
			"b": {Name: "b", Type: TypeString, Required: false},
		},
	}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// 后续写入立即按新版本校验：旧版本没有的属性 b 现在可写。
	mustWrite(t, s, Fact{
		ObjectID: "o1", ValidTime: 99, RecordTime: 200,
		Values: map[string]Value{
			"a": FloatValue(1.5),
			"b": StringValue("x"),
		},
	})
	// 历史展开立即可见新版本，且旧事实仍按旧版本解释。
	res, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1000, ValidFrom: 0, ValidTo: 100})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	for _, fv := range res.Facts {
		wantVer := int64(1)
		if fv.RecordTime >= 200 {
			wantVer = 2
		}
		if fv.SchemaVersionID != wantVer {
			t.Errorf("record=%d: version = %d, want %d", fv.RecordTime, fv.SchemaVersionID, wantVer)
		}
	}
}

// TestNewRequiredPropDistinction 新增必填属性生效前写入的事实：
// 该属性标记为 not-applicable（当时尚不存在）；生效后写入的事实
// 若遗漏取值，标记为 missing-required（当时已存在但遗漏）。
// 两种缺失不得混同。
func TestNewRequiredPropDistinction(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", map[string]PropertyDef{
		"a": {Name: "a", Type: TypeInt, Required: false},
	}, 0)
	mustRegister(t, s, "T", "o1")

	// 迁移前写入：r 尚不存在。
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 1, RecordTime: 100,
		Values: map[string]Value{"a": IntValue(1)}})

	// 迁移：新增可选属性 r（可选以便能写入遗漏 r 的事实）。
	if err := s.Migrate(Migration{
		TypeID: "T", EffectiveFrom: 200,
		NewProps: map[string]PropertyDef{
			"a": {Name: "a", Type: TypeInt, Required: false},
			"r": {Name: "r", Type: TypeInt, Required: true},
		},
	}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// 迁移后写入：r 已存在但遗漏 → missing-required。
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 2, RecordTime: 300,
		Values: map[string]Value{"a": IntValue(2)}})

	res, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 150, ValidFrom: 0, ValidTo: 10})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != 1 {
		t.Fatalf("got %d facts, want 1", len(res.Facts))
	}
	if got := res.Facts[0].Props["r"].Status; got != StatusNotApplicable {
		t.Fatalf("r status = %s, want not-applicable", got)
	}
	if got := res.Facts[0].Props["a"].Status; got != StatusValue {
		t.Fatalf("a status = %s, want value", got)
	}

	// 第二条事实：r 当时已存在但遗漏，标记必须不同于 not-applicable。
	res, err = s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 400, ValidFrom: 0, ValidTo: 10})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != 2 {
		t.Fatalf("got %d facts, want 2", len(res.Facts))
	}
	if got := res.Facts[1].Props["r"].Status; got != StatusMissingRequired {
		t.Fatalf("r status = %s, want missing-required", got)
	}
	if res.Facts[0].Props["r"].Status == res.Facts[1].Props["r"].Status {
		t.Fatal("not-applicable and missing-required must not be conflated")
	}
}
