package temporalauth

import "testing"

func TestErrorHelpers(t *testing.T) {
	if !HigherPriority(ErrWindowRuleInvalid, ErrViewerMissing) {
		t.Fatal("window rule must outrank viewer missing")
	}
	if HigherPriority(ErrViewerMissing, ErrRegionTimezoneUnresolved) {
		t.Fatal("viewer must not outrank region unresolved")
	}
	e := &AuthError{Code: ErrDenied}
	if e.Error() != string(ErrDenied) {
		t.Fatalf("error text %q", e.Error())
	}
	var err error = e
	got, ok := AsAuthError(err)
	if !ok || got.Code != ErrDenied {
		t.Fatalf("AsAuthError %v %v", got, ok)
	}
}

func TestRegionAndTypeVersionHelpers(t *testing.T) {
	r := &Region{ID: "R", Versions: []RegionVersion{
		{ValidFrom: 10, ZoneID: "A"},
		{ValidFrom: 20, ZoneID: "B"},
	}}
	if idx, ok := r.CurrentVersion(); !ok || idx != 1 {
		t.Fatalf("current %d %v", idx, ok)
	}
	if _, ok := r.EffectiveVersionAt(5); ok {
		t.Fatal("must be unresolved before first version")
	}
	if idx, ok := r.EffectiveVersionAt(20); !ok || idx != 1 {
		t.Fatalf("at 20 want idx 1 got %d %v", idx, ok)
	}

	empty := &Region{}
	if _, ok := empty.CurrentVersion(); ok {
		t.Fatal("empty region has no current version")
	}
	if err := (&Region{Versions: []RegionVersion{{ValidFrom: 1}, {ValidFrom: 1}}}).ValidateVersionChain(); err == nil {
		t.Fatal("non-strict version chain must fail validation")
	}

	ot := &ObjectType{Versions: []ObjectTypeVersion{
		{ValidFrom: 0, Properties: map[string]PropertySpec{"p": {Name: "p"}}},
	}}
	if _, ok := ot.VersionAt(0); !ok {
		t.Fatal("version at 0 must exist")
	}
	if _, ok := ot.PropertyAt("nope", 0); ok {
		t.Fatal("unknown property must not resolve")
	}
	if _, ok := ot.VersionAt(-1); ok {
		t.Fatal("negative time must not resolve a version")
	}

	rec := TemporalRecord{regionID: "R", basisVersion: 2}
	if rec.RegionID() != "R" || rec.BasisVersion() != 2 {
		t.Fatal("record accessors wrong")
	}
}

func TestWindowValidationAndAppendGuard(t *testing.T) {
	good := WindowRule{Start: Civil{1970, 1, 1, 0, 0, 0}, End: Civil{1970, 1, 2, 0, 0, 0}}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid window rejected: %v", err)
	}
	bad := WindowRule{Start: Civil{1970, 1, 2, 0, 0, 0}, End: Civil{1970, 1, 1, 0, 0, 0}}
	if err := bad.Validate(); err == nil {
		t.Fatal("window ending before start must be invalid")
	}
	same := WindowRule{Start: Civil{1970, 1, 1, 0, 0, 0}, End: Civil{1970, 1, 1, 0, 0, 0}}
	if err := same.Validate(); err == nil {
		t.Fatal("zero-length window must be invalid")
	}

	wc := &WindowChain{}
	if _, _, ok := wc.RuleAt(0); ok {
		t.Fatal("empty window chain must not resolve")
	}
	wc.Versions = []WindowVersion{{ValidFrom: 5, Rule: good}}
	if _, _, ok := wc.RuleAt(4); ok {
		t.Fatal("before first window version must not resolve")
	}

	store := NewStore(testCatalog())
	if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: 0, ZoneID: zStd}); err != nil {
		t.Fatal(err)
	}
	// 回溯式 ValidFrom 必须被拒绝。
	if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: 0, ZoneID: zEast}); err == nil {
		t.Fatal("non-monotonic region version must be rejected")
	}
	// 未知时区 ID 必须被拒绝。
	if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: 100, ZoneID: "NOPE"}); err == nil {
		t.Fatal("unknown zone id must be rejected")
	}
	// 非法窗口规则必须在追加时被拒绝。
	if err := store.AppendWindowVersion("ot", "p", WindowVersion{ValidFrom: 0, Rule: bad}); err == nil {
		t.Fatal("invalid window rule must be rejected on append")
	}

	store.AddRegion(&Region{ID: "S"})
	if store.Seq() < 0 {
		t.Fatal("seq must be non-negative")
	}

	// 在无任何地区版本的地区录入记录，必须报地区时区无法确定。
	store.RegisterObject("obj", "ot", "S")
	if _, err := store.PutTemporalRecord(TemporalRecord{
		ObjectID: "obj", Property: "p", RecordedAt: 1, Wall: Civil{Year: 1970, Month: 1, Day: 1},
	}); err == nil {
		t.Fatal("recording against unresolved region must fail")
	}
}
