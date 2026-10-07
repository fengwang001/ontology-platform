package temporalauth

import (
	"strings"
	"testing"
)

const (
	zStd  = "STD"  // 恒定 +0
	zEast = "EAST" // 恒定 +2
	zDst  = "DST"  // 有春秋切换
)

func testCatalog() *ZoneCatalog {
	std := &ZoneRules{ID: zStd, BaseOffset: 0}
	east := &ZoneRules{ID: zEast, BaseOffset: 2 * 3600}
	dst := &ZoneRules{
		ID:         zDst,
		BaseOffset: 0,
		Transitions: []Transition{
			{At: 1711846800, OffsetAfter: 3600},
			{At: 1729990800, OffsetAfter: 0},
		},
	}
	return NewZoneCatalog(std, east, dst)
}

func setupService(t *testing.T) (*Service, *Store, *AuditLog) {
	t.Helper()
	store := NewStore(testCatalog())
	audit := NewAuditLog()
	svc := NewService(store, audit)

	store.AddObjectType(&ObjectType{
		ID: "ot",
		Versions: []ObjectTypeVersion{
			{ValidFrom: 0, Properties: map[string]PropertySpec{
				"p": {Name: "p", Kind: KindTemporal},
				"d": {Name: "d", Kind: KindTemporal},
			}},
			{ValidFrom: 5000, Properties: map[string]PropertySpec{
				"p": {Name: "p", Kind: KindTemporal},
				"d": {Name: "d", Kind: KindTemporal, Deprecated: true},
			}},
		},
	})

	// 地区在 t=1000 从 STD 迁移到 EAST（地区默认时区定义版本迁移）。
	if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: 0, ZoneID: zStd}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: 1000, ZoneID: zEast}); err != nil {
		t.Fatal(err)
	}

	store.RegisterObject("obj", "ot", "R")

	// 窗口规则：以地区默认时区表述，本地 00:00:00 到 00:00:02（半开）。
	if err := store.AppendWindowVersion("ot", "p", WindowVersion{
		ValidFrom: 0,
		Rule:      WindowRule{RegionID: "R", Start: Civil{1970, 1, 1, 0, 0, 0}, End: Civil{1970, 1, 1, 0, 0, 2}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendWindowVersion("ot", "d", WindowVersion{
		ValidFrom: 0,
		Rule:      WindowRule{RegionID: "R", Start: Civil{1970, 1, 1, 0, 0, 0}, End: Civil{1970, 1, 1, 0, 0, 2}},
	}); err != nil {
		t.Fatal(err)
	}

	return svc, store, audit
}

func TestBasisFreezeHistoricalRecord(t *testing.T) {
	svc, store, _ := setupService(t)

	// 在迁移前（t=500，STD 生效）录入一条墙钟 00:00:10 的记录。
	rec, err := store.PutTemporalRecord(TemporalRecord{
		ObjectID: "obj", Property: "p", RecordedAt: 500,
		RecordZone: zDst, Wall: Civil{1970, 1, 1, 0, 0, 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 归一化基准必须冻结为迁移前的 STD 版本（下标 0），归一化值=10。
	if rec.BasisVersion() != 0 || rec.NormalizedAt() != 10 {
		t.Fatalf("frozen basis=%d norm=%d", rec.BasisVersion(), rec.NormalizedAt())
	}

	// 迁移后（t=2000，EAST 生效）同一记录反复判定，审计中的基准版本与归一化
	// 取值不得随当前时间摇摆。
	for i := 0; i < 3; i++ {
		d := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
			Viewer: &Viewer{ID: "v"}, Now: 2000})
		// 2000 落在窗口（迁移后 EAST：本地 00:00:00..00:00:02 == UTC 前一日 22:00:00..22:00:02）之外。
		if d.Allowed {
			t.Fatal("expected deny outside window after migration")
		}
	}
}

func TestWindowBoundaryHalfOpen(t *testing.T) {
	svc, _, _ := setupService(t)

	if _, err := storePut(svc, "p", 0); err != nil {
		t.Fatal(err)
	}

	// STD 生效（now<1000）：窗口本地 [00:00:00,00:00:02) == UTC [0,2)。
	// 左边界 now=0 归窗内，右边界 now=2 归窗外。
	in := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: 0})
	if !in.Allowed {
		t.Fatalf("left boundary must be inside, code=%s", in.Code)
	}
	out := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: 2})
	if out.Allowed || out.Code != ErrDenied {
		t.Fatalf("right boundary must be outside, allowed=%v code=%s", out.Allowed, out.Code)
	}
}

func TestErrorPriorityAndCodes(t *testing.T) {
	svc, store, _ := setupService(t)

	// 查询者缺失 + 属性已废弃同时成立 -> 报告 VIEWER_MISSING（优先级更高）。
	d := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "d",
		Viewer: nil, Now: 6000})
	if d.Code != ErrViewerMissing {
		t.Fatalf("want VIEWER_MISSING got %s", d.Code)
	}

	// 查询者在场 + 属性废弃 -> PROPERTY_DEPRECATED。
	d = svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "d",
		Viewer: &Viewer{ID: "v"}, Now: 6000})
	if d.Code != ErrPropertyDeprecated {
		t.Fatalf("want PROPERTY_DEPRECATED got %s", d.Code)
	}

	// 地区无任何生效版本（新建空地区对象）-> REGION_TIMEZONE_UNRESOLVED。
	store.RegisterObject("orphan", "ot", "EMPTY")
	d = svc.View(ViewRequest{ObjectID: "orphan", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: 10})
	if d.Code != ErrRegionTimezoneUnresolved {
		t.Fatalf("want REGION_TIMEZONE_UNRESOLVED got %s", d.Code)
	}

	// 无窗口规则的属性 -> WINDOW_RULE_INVALID（即使查询者缺失，规则优先级更高）。
	d = svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "norule",
		Viewer: nil, Now: 10})
	if d.Code != ErrWindowRuleInvalid {
		t.Fatalf("want WINDOW_RULE_INVALID got %s", d.Code)
	}
}

func TestNoLeakageOnDenyOrError(t *testing.T) {
	svc, _, _ := setupService(t)
	if _, err := storePut(svc, "p", 0); err != nil {
		t.Fatal(err)
	}

	cases := []Decision{
		svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
			Viewer: &Viewer{ID: "v"}, Now: 99999}), // 拒绝
		svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "d",
			Viewer: nil, Now: 6000}), // 错误
	}
	for _, d := range cases {
		if d.Value != nil {
			t.Fatalf("denied/error decision must not carry value: %+v", d)
		}
		msg := d.Code
		for _, secret := range []string{"EAST", "STD", "DST", "00:00", "1970"} {
			if strings.Contains(string(msg), secret) {
				t.Fatalf("response code leaks %q: %q", secret, msg)
			}
		}
	}
}

func TestAuditRecordsBasisAndCrossCheck(t *testing.T) {
	svc, _, audit := setupService(t)
	if _, err := storePut(svc, "p", 0); err != nil {
		t.Fatal(err)
	}

	svc.View(ViewRequest{RequestID: "r1", ObjectID: "obj", ObjectType: "ot",
		Property: "p", Viewer: &Viewer{ID: "v"}, Now: 0})

	entries := audit.Entries()
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry got %d", len(entries))
	}
	e := entries[0]
	if !e.CrossCheck || !e.CrossMatch {
		t.Fatalf("naive cross-check mismatch: %+v", e)
	}
	if e.BasisZoneID != zStd || e.BasisVersion != 0 {
		t.Fatalf("audit basis wrong: %+v", e)
	}
	if e.WindowStart != 0 || e.WindowEnd != 2 {
		t.Fatalf("audit window wrong: %+v", e)
	}
	if e.Outcome != "allow" {
		t.Fatalf("audit outcome wrong: %+v", e)
	}
}

// storePut 通过服务所在 store 录入一条记录（测试辅助）。
func storePut(svc *Service, prop string, recordedAt Instant) (TemporalRecord, error) {
	return svc.store.PutTemporalRecord(TemporalRecord{
		ObjectID: "obj", Property: prop, RecordedAt: recordedAt,
		RecordZone: zDst, Wall: Civil{1970, 1, 1, 0, 0, 1},
	})
}
