package temporalauth

import "testing"

// TestWindowAcrossDSTBoundary 规定：窗口可跨越夏令时切换当天，边界在归一化
// 后仍按半开 [start,end) 处理，Start 在内、End 在外，不因 DST 产生特例。
func TestWindowAcrossDSTBoundary(t *testing.T) {
	rules := testZone()
	rules.ID = zDst
	catalog := NewZoneCatalog(rules)
	store := NewStore(catalog)
	audit := NewAuditLog()
	svc := NewService(store, audit)

	store.AddObjectType(&ObjectType{ID: "ot", Versions: []ObjectTypeVersion{
		{ValidFrom: 0, Properties: map[string]PropertySpec{
			"p": {Name: "p", Kind: KindTemporal},
		}},
	}})
	if err := store.AppendRegionVersion("R", RegionVersion{ValidFrom: 0, ZoneID: zDst}); err != nil {
		t.Fatal(err)
	}
	store.RegisterObject("obj", "ot", "R")

	// 窗口横跨春季切换：本地 2024-03-31 00:30:00 到 03:30:00。
	if err := store.AppendWindowVersion("ot", "p", WindowVersion{
		ValidFrom: 0,
		Rule: WindowRule{
			RegionID: "R",
			Start:    Civil{2024, 3, 31, 0, 30, 0},
			End:      Civil{2024, 3, 31, 3, 30, 0},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutTemporalRecord(TemporalRecord{
		ObjectID: "obj", Property: "p", RecordedAt: 1, RecordZone: zStd,
		Wall: Civil{1970, 1, 1, 0, 0, 0},
	}); err != nil {
		t.Fatal(err)
	}

	r := testZone()
	start := r.CivilToInstant(Civil{2024, 3, 31, 0, 30, 0})
	end := r.CivilToInstant(Civil{2024, 3, 31, 3, 30, 0})

	// 左边界（绝对时刻）必须在窗内。
	if d := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: start}); !d.Allowed {
		t.Fatalf("start boundary must be inside: %s", d.Code)
	}
	// 右边界（绝对时刻）必须在窗外。
	if d := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: end}); d.Allowed || d.Code != ErrDenied {
		t.Fatalf("end boundary must be outside: allowed=%v code=%s", d.Allowed, d.Code)
	}
	// 跃迁点本身落在窗口内（非特例，仅因其处于 (start,end)）。
	spring := Instant(1711846800)
	if d := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: spring}); !d.Allowed {
		t.Fatalf("spring-forward instant inside spanning window must be allowed: %s", d.Code)
	}
	// 窗口前一刻与窗口后一刻均拒绝。
	if d := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: start - 1}); d.Allowed {
		t.Fatal("instant before start must be denied")
	}
	if d := svc.View(ViewRequest{ObjectID: "obj", ObjectType: "ot", Property: "p",
		Viewer: &Viewer{ID: "v"}, Now: end + 1}); d.Allowed {
		t.Fatal("instant after end must be denied")
	}
}
