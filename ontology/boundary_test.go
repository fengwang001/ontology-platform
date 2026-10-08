package ontology

import "testing"

func mustCreate(t *testing.T, s *Store, typeID string, props map[string]PropertyDef, from RecordTime) {
	t.Helper()
	if err := s.CreateObjectType(typeID, props, from); err != nil {
		t.Fatalf("CreateObjectType: %v", err)
	}
}

func mustRegister(t *testing.T, s *Store, typeID, objectID string) {
	t.Helper()
	if err := s.RegisterObject(typeID, objectID); err != nil {
		t.Fatalf("RegisterObject: %v", err)
	}
}

func mustWrite(t *testing.T, s *Store, f Fact) {
	t.Helper()
	if err := s.WriteFact(f); err != nil {
		t.Fatalf("WriteFact(%+v): %v", f, err)
	}
}

func simpleProps() map[string]PropertyDef {
	return map[string]PropertyDef{
		"a": {Name: "a", Type: TypeInt, Required: true},
	}
}

// TestSchemaBoundaryEquality 穷举版本边界取等情形：
// 记录时刻落在边界上时一律取右侧（新）版本（左闭右开）。
func TestSchemaBoundaryEquality(t *testing.T) {
	const boundary RecordTime = 500
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 100)
	mustRegister(t, s, "T", "o1")

	// 迁移：属性 a 由 int 放宽为 float，自 500（含）生效。
	if err := s.Migrate(Migration{
		TypeID: "T", EffectiveFrom: boundary,
		NewProps: map[string]PropertyDef{
			"a": {Name: "a", Type: TypeFloat, Required: true},
		},
	}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// 边界两侧各取等/邻接点写入事实。
	cases := []struct {
		rt      RecordTime
		val     Value
		wantVer int64
		wantVal Value
	}{
		{499, IntValue(1), 1, IntValue(1)},         // 边界前一刻：旧版本
		{500, FloatValue(2.5), 2, FloatValue(2.5)}, // 恰在边界：新版本
		{501, FloatValue(3.5), 2, FloatValue(3.5)}, // 边界后一刻：新版本
	}
	for i, c := range cases {
		mustWrite(t, s, Fact{
			ObjectID: "o1", ValidTime: ValidTime(10 + i), RecordTime: c.rt,
			Values: map[string]Value{"a": c.val},
		})
	}

	res, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1000, ValidFrom: 0, ValidTo: 1000})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != len(cases) {
		t.Fatalf("got %d facts, want %d", len(res.Facts), len(cases))
	}
	for i, c := range cases {
		fv := res.Facts[i]
		if fv.SchemaVersionID != c.wantVer {
			t.Errorf("rt=%d: schema version = %d, want %d", c.rt, fv.SchemaVersionID, c.wantVer)
		}
		got := fv.Props["a"]
		if got.Status != StatusValue || !got.Value.Equal(c.wantVal) {
			t.Errorf("rt=%d: prop a = %+v, want value %+v", c.rt, got, c.wantVal)
		}
	}
}

// TestRecordTimeMonotonic 记录时间轴单调不倒退；相等允许（修正轨迹）。
func TestRecordTimeMonotonic(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 0)
	mustRegister(t, s, "T", "o1")

	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 5, RecordTime: 100,
		Values: map[string]Value{"a": IntValue(1)}})
	// 相等记录时刻：允许，构成同一有效时间的修正。
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 5, RecordTime: 100,
		Values: map[string]Value{"a": IntValue(2)}})
	// 倒退：拒绝。
	if err := s.WriteFact(Fact{ObjectID: "o1", ValidTime: 5, RecordTime: 99,
		Values: map[string]Value{"a": IntValue(3)}}); err == nil {
		t.Fatal("expected error for backward record time")
	}

	// 同一有效时间的多条修正，取记录时刻及之前最新一条。
	res, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 100, ValidFrom: 5, ValidTo: 5})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != 1 {
		t.Fatalf("got %d facts, want 1", len(res.Facts))
	}
	if got := res.Facts[0].Props["a"].Value; !got.Equal(IntValue(2)) {
		t.Errorf("correction not picked: got %+v, want 2", got)
	}
}

// TestAsOfVisibilityEquality 展开可见性取等：asOf == 记录时刻时可见，
// asOf == 记录时刻 - 1 时不可见。
func TestAsOfVisibilityEquality(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 0)
	mustRegister(t, s, "T", "o1")
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 7, RecordTime: 100,
		Values: map[string]Value{"a": IntValue(1)}})
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 8, RecordTime: 200,
		Values: map[string]Value{"a": IntValue(2)}})

	// asOf = 199：第二条不可见。
	res, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 199, ValidFrom: 0, ValidTo: 100})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != 1 || res.Facts[0].ValidTime != 7 {
		t.Fatalf("asOf=199: got %+v, want only valid=7", res.Facts)
	}
	// asOf = 200：恰等记录时刻，可见。
	res, err = s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 200, ValidFrom: 0, ValidTo: 100})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != 2 {
		t.Fatalf("asOf=200: got %d facts, want 2", len(res.Facts))
	}
}

// TestValidRangeInclusive 有效时间区间双端闭区间取等。
func TestValidRangeInclusive(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 0)
	mustRegister(t, s, "T", "o1")
	for i, vt := range []ValidTime{10, 20, 30} {
		mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: vt, RecordTime: RecordTime(100 + i),
			Values: map[string]Value{"a": IntValue(int64(vt))}})
	}
	res, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1000, ValidFrom: 10, ValidTo: 30})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != 3 {
		t.Fatalf("inclusive range: got %d facts, want 3", len(res.Facts))
	}
	res, err = s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1000, ValidFrom: 11, ValidTo: 29})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(res.Facts) != 1 || res.Facts[0].ValidTime != 20 {
		t.Fatalf("interior range: got %+v, want only valid=20", res.Facts)
	}
}

// TestMigrationBoundaryEquality 迁移生效时刻取等：
// EffectiveFrom 恰等于既有版本 From 时旧版本整体作废；
// 恰等于两版本边界时无截断发生。
func TestMigrationBoundaryEquality(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 100)
	mustRegister(t, s, "T", "o1")

	// EffectiveFrom == 初始版本 From：初始版本被完全遮蔽作废。
	if err := s.Migrate(Migration{
		TypeID: "T", EffectiveFrom: 100,
		NewProps: map[string]PropertyDef{
			"b": {Name: "b", Type: TypeString, Required: false},
		},
	}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, ok := s.types["T"].findVersion(1); ok {
		t.Fatal("version 1 should have been invalidated")
	}

	// 连续两次迁移到同一边界：第二次 EffectiveFrom == 第一次 From，
	// 第一次的版本被作废，边界方向保持一致。
	if err := s.Migrate(Migration{
		TypeID: "T", EffectiveFrom: 500,
		NewProps: map[string]PropertyDef{
			"c": {Name: "c", Type: TypeInt, Required: false},
		},
	}); err != nil {
		t.Fatalf("Migrate 2: %v", err)
	}
	v3From := RecordTime(500)
	if err := s.Migrate(Migration{
		TypeID: "T", EffectiveFrom: v3From,
		NewProps: map[string]PropertyDef{
			"d": {Name: "d", Type: TypeInt, Required: false},
		},
	}); err != nil {
		t.Fatalf("Migrate 3: %v", err)
	}
	ts := s.types["T"]
	if _, ok := ts.findVersion(3); ok {
		t.Fatal("version 3 should have been invalidated by equal-boundary migration")
	}
	if _, ok := ts.findVersion(4); !ok {
		t.Fatal("version 4 should exist")
	}
	// 边界上的记录时刻归属新版本（版本 4）。
	if v, ok := ts.versionAt(500, nil); !ok || v.ID != 4 {
		t.Fatalf("versionAt(500) = %+v, want version 4", v)
	}
	if v, ok := ts.versionAt(499, nil); !ok || v.ID != 2 {
		t.Fatalf("versionAt(499) = %+v, want version 2", v)
	}
}

// TestErrorPriority 同一次请求同时具备多类错误条件时，
// 按优先级只报告一类。
func TestErrorPriority(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 100)
	mustRegister(t, s, "T", "o1")
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 10, RecordTime: 200,
		Values: map[string]Value{"a": IntValue(1)}})

	pin := int64(999) // 不存在的版本
	// 同时触发：记录时刻过早(1) + 版本作废(2) + 区间矛盾(3) → 只报 1。
	_, err := s.Expand(ExpandRequest{
		ObjectID: "o1", AsOfRecord: 50, ValidFrom: 100, ValidTo: 1,
		PinSchemaVersion: &pin,
	})
	assertErrCode(t, err, ErrCodeRecordBeforeFirstFact)

	// 同时触发：版本作废(2) + 区间矛盾(3) → 只报 2。
	_, err = s.Expand(ExpandRequest{
		ObjectID: "o1", AsOfRecord: 300, ValidFrom: 100, ValidTo: 1,
		PinSchemaVersion: &pin,
	})
	assertErrCode(t, err, ErrCodeSchemaInvalidated)

	// 仅区间矛盾(3)。
	_, err = s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 300, ValidFrom: 100, ValidTo: 1})
	assertErrCode(t, err, ErrCodeContradictoryRange)
}

// TestErrorNoSideEffect 任一错误发生时不得产生可观察改动。
func TestErrorNoSideEffect(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "T", simpleProps(), 0)
	mustRegister(t, s, "T", "o1")
	mustWrite(t, s, Fact{ObjectID: "o1", ValidTime: 10, RecordTime: 100,
		Values: map[string]Value{"a": IntValue(1)}})

	before, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1000, ValidFrom: 0, ValidTo: 100})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	versionsBefore := len(s.types["T"].versions)
	// 触发各类错误。
	_, _ = s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1, ValidFrom: 0, ValidTo: 100})
	_, _ = s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1000, ValidFrom: 50, ValidTo: 1})
	_ = s.Migrate(Migration{TypeID: "T", EffectiveFrom: 0, NewProps: map[string]PropertyDef{
		"a": {Name: "a", Type: TypeInt, Required: true},
		"r": {Name: "r", Type: TypeInt, Required: true}, // 既有事实缺少 r → 校验失败
	}})

	after, err := s.Expand(ExpandRequest{ObjectID: "o1", AsOfRecord: 1000, ValidFrom: 0, ValidTo: 100})
	if err != nil {
		t.Fatalf("Expand after errors: %v", err)
	}
	if len(after.Facts) != len(before.Facts) {
		t.Fatal("facts changed after failed operations")
	}
	if len(s.types["T"].versions) != versionsBefore {
		t.Fatal("schema versions changed after failed operations")
	}
}

func assertErrCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("err = %v, want *Error with code %s", err, code)
	}
	if e.Code != code {
		t.Fatalf("err code = %s, want %s", e.Code, code)
	}
}
