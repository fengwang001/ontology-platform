package temporalauth

import (
	"math/rand"
	"testing"
)

// TestRandomizedCrossCheck 在随机操作序列下，逐条对照生产路径与独立朴素
// 归一化模型的结论（允许/拒绝 + 错误代码）必须完全一致。
func TestRandomizedCrossCheck(t *testing.T) {
	catalog := NewZoneCatalog(
		&ZoneRules{ID: "A", BaseOffset: 0},
		&ZoneRules{ID: "B", BaseOffset: 3600},
		&ZoneRules{ID: "C", BaseOffset: 7200},
		&ZoneRules{ID: "D", BaseOffset: 0, Transitions: []Transition{
			{At: 100000, OffsetAfter: 3600},
			{At: 200000, OffsetAfter: 0},
		}},
	)

	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		store := NewStore(catalog)
		audit := NewAuditLog()
		svc := NewService(store, audit)

		// 随机挑选若干时区构造地区版本迁移序列（ValidFrom 严格递增）。
		zones := []string{"A", "B", "C", "D"}
		regionID := "R"
		vt := Instant(0)
		for k := 0; k < 1+rng.Intn(5); k++ {
			z := zones[rng.Intn(len(zones))]
			if err := store.AppendRegionVersion(regionID, RegionVersion{ValidFrom: vt, ZoneID: z}); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			vt += Instant(1000 + rng.Intn(5000))
		}

		objCount := 2
		for o := 0; o < objCount; o++ {
			store.RegisterObject(objectName(o), "ot", regionID)
		}

		// 当前对象类型版本（属性可能被废弃）。
		deprecated := map[string]bool{}
		curOT := Instant(0)

		props := []string{"p0", "p1", "p2"}
		for _, p := range props {
			deprecated[p] = false
		}
		initialSpecs := map[string]PropertySpec{}
		for _, p := range props {
			initialSpecs[p] = PropertySpec{Name: p, Kind: KindTemporal}
		}
		store.AddObjectType(&ObjectType{ID: "ot", Versions: []ObjectTypeVersion{
			{ValidFrom: 0, Properties: initialSpecs},
		}})

		// 为每个属性随机生成 1..3 个窗口规则版本。
		wt := map[string]Instant{}
		for _, p := range props {
			wt[p] = 0
		}
		ensureWindow := func(p string, now Instant) {
			for wt[p] <= now {
				startDay := int64(now) / 86400
				startSec := rng.Int63n(86400)
				start := startDay*86400 + startSec
				length := int64(1 + rng.Intn(43200))
				startCivil := secondsToCivil(start)
				endCivil := secondsToCivil(start + length)
				err := store.AppendWindowVersion("ot", p, WindowVersion{
					ValidFrom: wt[p],
					Rule:      WindowRule{RegionID: regionID, Start: startCivil, End: endCivil},
				})
				if err != nil {
					t.Fatalf("seed %d window: %v", seed, err)
				}
				wt[p] += Instant(1 + rng.Intn(20000))
			}
		}

		// 随机操作：录入记录 / 废弃属性 / 查看。
		for step := 0; step < 400; step++ {
			o := rng.Intn(objCount)
			p := props[rng.Intn(len(props))]
			now := Instant(rng.Intn(60000))
			switch rng.Intn(6) {
			case 0, 1: // 录入
				snap := store.Snapshot()
				reg := snap.regions[regionID]
				if idx, ok := reg.EffectiveVersionAt(now); ok {
					zoneID := reg.Versions[idx].ZoneID
					zone, _ := catalog.Get(zoneID)
					wall := zone.InstantToCivil(now + Instant(rng.Intn(3600)))
					_, _ = store.PutTemporalRecord(TemporalRecord{
						ObjectID: objectName(o), Property: p, RecordedAt: now,
						RecordZone: zones[rng.Intn(len(zones))], Wall: wall,
					})
				}
			case 2: // 追加对象类型版本废弃某属性
				curOT += 100
				newDeps := map[string]bool{}
				for _, q := range props {
					newDeps[q] = deprecated[q] || (q == p && rng.Intn(2) == 0)
				}
				deprecated = newDeps
				specs := map[string]PropertySpec{}
				for q, dep := range newDeps {
					specs[q] = PropertySpec{Name: q, Kind: KindTemporal, Deprecated: dep}
				}
				if err := store.AppendObjectTypeVersion("ot",
					ObjectTypeVersion{ValidFrom: curOT, Properties: specs}); err != nil {
					t.Fatal(err)
				}
			default: // 查看
				ensureWindow(p, now)
				var viewer *Viewer
				if rng.Intn(5) != 0 {
					viewer = &Viewer{ID: "v", ZoneID: zones[rng.Intn(len(zones))]}
				}
				req := ViewRequest{ObjectID: objectName(o), ObjectType: "ot", Property: p,
					Viewer: viewer, Now: now}
				d := svc.View(req)

				naive := svc.naive.Decide(store.Snapshot(), req)
				if d.Allowed != naive.Allowed || d.Code != naive.Code {
					t.Fatalf("seed %d step %d mismatch: prod(%v,%s) naive(%v,%s)",
						seed, step, d.Allowed, d.Code, naive.Allowed, naive.Code)
				}
			}
		}

		// 审计中所有对照必须一致。
		for _, e := range audit.Entries() {
			if e.CrossCheck && !e.CrossMatch {
				t.Fatalf("seed %d audit cross mismatch: %+v", seed, e)
			}
		}
	}
}

func objectName(i int) string {
	return "obj" + string(rune('0'+i))
}
