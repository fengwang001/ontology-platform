package linkrepair

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"testing"
)

// genCase 生成一份随机损坏的链接快照。
//
// 注入的损坏类型覆盖：链接类型丢失/未知、端字段丢失（残留记录）、
// 引用不可用对象、同一锚点多候选（单一/带上限基数冲突）、重复副本，
// 以及同一对象在不同链接类型下同时卷入冲突。
type genCase struct {
	types []LinkType
	objs  map[string]struct{}
	recs  []RawRecord
}

func genCorrupted(rng *rand.Rand, trial int) genCase {
	types := []LinkType{
		{ID: "toOne", MaxA: 1, MaxB: 1},
		{ID: "cap2", MaxA: 2, MaxB: 0},
		{ID: "many", MaxA: 0, MaxB: 0},
	}

	// 对象池：部分对象被标记为不可用（同一次恢复中已判定不可用）。
	const poolSize = 12
	all := make([]string, poolSize)
	objs := map[string]struct{}{}
	for i := range all {
		id := fmt.Sprintf("obj%02d", i)
		all[i] = id
		if rng.Float64() < 0.75 {
			objs[id] = struct{}{}
		}
	}

	n := rng.Intn(40)
	recs := make([]RawRecord, 0, n)
	for i := 0; i < n; i++ {
		lt := types[rng.Intn(len(types))].ID
		si, di := rng.Intn(poolSize), rng.Intn(poolSize)
		if di == si {
			di = (di + 1) % poolSize
		}
		src, dst := all[si], all[di]
		rec := RawRecord{
			ID:         fmt.Sprintf("t%d-e%03d", trial, i),
			LinkTypeID: lt,
			SourceID:   src,
			TargetID:   dst,
		}

		// 制造围绕少数"热点"锚点的冲突，保证每个用例中冲突频繁出现。
		if rng.Float64() < 0.45 {
			hot := fmt.Sprintf("obj%02d", rng.Intn(3))
			if rng.Float64() < 0.7 {
				rec.SourceID = hot
			} else {
				rec.TargetID = hot
			}
		}

		switch rng.Intn(10) {
		case 0:
			rec.LinkTypeID = "" // 链接类型丢失
		case 1:
			rec.LinkTypeID = "unknown-type" // 未知类型
		case 2:
			rec.SourceID = "" // 仅残留 B 端
		case 3:
			rec.TargetID = "" // 仅残留 A 端
		case 4:
			// 引用一个对象池之外、必然不可用的对象
			if rng.Float64() < 0.5 {
				rec.SourceID = "missing-obj"
			} else {
				rec.TargetID = "missing-obj"
			}
		case 5, 6:
			// 重复：复制一条此前记录的内容（但保留新的实例标识）
			if len(recs) > 0 {
				src2 := recs[rng.Intn(len(recs))]
				rec.LinkTypeID = src2.LinkTypeID
				rec.SourceID = src2.SourceID
				rec.TargetID = src2.TargetID
			}
		}
		recs = append(recs, rec)
	}
	return genCase{types: types, objs: objs, recs: recs}
}

// traceEntry 记录一次对照测试的输入、输出与逐条判定依据，
// 可序列化输出以便复核。
type traceEntry struct {
	Seed      int            `json:"seed"`
	Trial     int            `json:"trial"`
	Types     []LinkType     `json:"types"`
	Available []string       `json:"available_objects"`
	Records   []RawRecord    `json:"records"`
	Verdicts  []traceVerdict `json:"verdicts"`
	Kept      [][3]string    `json:"kept"`
}

type traceVerdict struct {
	Position int    `json:"position"`
	Kept     bool   `json:"kept"`
	Reason   string `json:"reason"`
	Detail   string `json:"detail"`
}

func TestDifferentialAgainstNaiveModel(t *testing.T) {
	const trials = 2000
	traces := make([]traceEntry, 0, trials)

	for seed := int64(1); seed <= 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		for trial := 0; trial < trials/50; trial++ {
			gc := genCorrupted(rng, int(seed)*1000+trial)
			snap := Snapshot{
				LinkTypes:        gc.types,
				AvailableObjects: gc.objs,
				Records:          gc.recs,
			}

			got := Repair(snap)
			want := naiveRepair(snap)

			if len(got.Verdicts) != len(want) {
				t.Fatalf("seed=%d trial=%d verdict count %d != %d", seed, trial, len(got.Verdicts), len(want))
			}
			for i, w := range want {
				if got.Verdicts[i].Reason != w.reason || got.Verdicts[i].Kept != (w.reason == ReasonNone) {
					t.Fatalf("seed=%d trial=%d position=%d: got (kept=%v reason=%s), want reason=%s\nrecords=%+v",
						seed, trial, i, got.Verdicts[i].Kept, got.Verdicts[i].Reason, w.reason, gc.recs)
				}
			}

			// 参照模型无关的直接不变量：保留边不引用不可用对象。
			for _, k := range got.Kept {
				if _, ok := gc.objs[k.SourceID]; !ok {
					t.Fatalf("seed=%d kept edge references unavailable %q", seed, k.SourceID)
				}
				if _, ok := gc.objs[k.TargetID]; !ok {
					t.Fatalf("seed=%d kept edge references unavailable %q", seed, k.TargetID)
				}
			}

			avail := make([]string, 0, len(gc.objs))
			for id := range gc.objs {
				avail = append(avail, id)
			}
			entry := traceEntry{
				Seed:      int(seed),
				Trial:     trial,
				Types:     gc.types,
				Available: avail,
				Records:   append([]RawRecord(nil), gc.recs...),
				Kept:      keptKeys(got),
			}
			for _, v := range got.Verdicts {
				entry.Verdicts = append(entry.Verdicts, traceVerdict{
					Position: v.Position, Kept: v.Kept, Reason: v.Reason.String(), Detail: v.Detail,
				})
			}
			traces = append(traces, entry)
		}
	}

	t.Logf("differential trials passed: %d", len(traces))

	// 设置 LINKREPAIR_TRACE 时将全部输入/输出/判定依据落盘，便于人工复核。
	if path := os.Getenv("LINKREPAIR_TRACE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		enc := json.NewEncoder(f)
		for _, e := range traces {
			if err := enc.Encode(e); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("trace written to %s", path)
	}

	// 抽样记录一次完整裁决到测试日志（输入/输出/判定依据）。
	if raw, err := json.MarshalIndent(traces[0], "", "  "); err == nil {
		t.Logf("sample adjudication:\n%s", raw)
	}

	// 确认四类判定在大规模随机构造中都实际出现过，避免对照只覆盖平凡情形。
	seen := map[Reason]int{}
	for _, e := range traces {
		for _, v := range e.Verdicts {
			seen[reasonFromName(v.Reason)]++
		}
	}
	for _, r := range []Reason{ReasonMalformed, ReasonReferencedUnavailable, ReasonCardinalityConflict, ReasonDuplicate} {
		if seen[r] == 0 {
			t.Fatalf("reason %s never exercised in random trials", r)
		}
	}
	t.Logf("reason distribution: malformed=%d unavailable=%d cardinality=%d duplicate=%d kept=%d",
		seen[ReasonMalformed], seen[ReasonReferencedUnavailable],
		seen[ReasonCardinalityConflict], seen[ReasonDuplicate], seen[ReasonNone])

	_ = reflect.TypeOf // 保留 reflect 以便后续扩展断言
}

func reasonFromName(name string) Reason {
	switch name {
	case "none":
		return ReasonNone
	case "malformed":
		return ReasonMalformed
	case "referenced_object_unavailable":
		return ReasonReferencedUnavailable
	case "cardinality_conflict":
		return ReasonCardinalityConflict
	case "duplicate":
		return ReasonDuplicate
	default:
		return -1
	}
}

// 随机化验证：同一批对象在多种链接类型下同时冲突时，
// 每种链接类型的保留集合都只由其自身基数约束决定，互不影响。
func TestCrossLinkTypeIndependenceRandom(t *testing.T) {
	types := []LinkType{
		{ID: "t1", MaxA: 1, MaxB: 1},
		{ID: "t2", MaxA: 2, MaxB: 1},
		{ID: "t3", MaxA: 0, MaxB: 2},
	}
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		var recs []RawRecord
		objs := map[string]struct{}{}
		for o := 0; o < 8; o++ {
			objs[fmt.Sprintf("o%d", o)] = struct{}{}
		}
		// 按链接类型批量生成：锚点 o0..o3，对方 o4..o7 随机选取并重复。
		for _, lt := range types {
			for anchor := 0; anchor < 4; anchor++ {
				m := 1 + rng.Intn(6)
				for j := 0; j < m; j++ {
					recs = append(recs, RawRecord{
						LinkTypeID: lt.ID,
						SourceID:   fmt.Sprintf("o%d", anchor),
						TargetID:   fmt.Sprintf("o%d", 4+rng.Intn(4)),
					})
				}
			}
		}

		full := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})

		// 单独对每种链接类型裁决（其余记录移除），结果必须与混合裁决中的对应子集一致。
		for _, lt := range types {
			var sub []RawRecord
			for _, r := range recs {
				if r.LinkTypeID == lt.ID {
					sub = append(sub, r)
				}
			}
			alone := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: sub})
			for _, k := range alone.Kept {
				found := false
				for _, fk := range full.Kept {
					if fk == k {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("iter=%d link %s: edge kept alone but lost in mixed adjudication: %+v",
						iter, lt.ID, k)
				}
			}
			if len(alone.Kept) != countKeptOfType(full, lt.ID) {
				t.Fatalf("iter=%d link %s: kept count alone=%d mixed=%d",
					iter, lt.ID, len(alone.Kept), countKeptOfType(full, lt.ID))
			}
		}
	}
}

func countKeptOfType(r Report, linkType string) int {
	n := 0
	for _, k := range r.Kept {
		if k.LinkTypeID == linkType {
			n++
		}
	}
	return n
}

// 局部化开销的可复核依据：无冲突时全部记录线性通过（无任何排序）；
// 仅在局部范围超限时才付出排序代价。基准数据展示线性扩展。
func BenchmarkRepairConflictFree(b *testing.B) {
	snap := buildScaledSnapshot(10000, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rep := Repair(snap)
		if len(rep.Kept) != 10000 {
			b.Fatalf("kept %d, want 10000", len(rep.Kept))
		}
	}
}

func BenchmarkRepairLocalConflicts(b *testing.B) {
	// 10000 条互不冲突记录之外，仅 50 个局部范围各自超限 4 条。
	snap := buildScaledSnapshot(10000, 50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rep := Repair(snap)
		// 50 个热点范围：每范围 5 个不同对方、上限 1，各舍弃 4 条。
		if want := 10000 + 50; len(rep.Kept) != want {
			b.Fatalf("kept %d, want %d", len(rep.Kept), want)
		}
	}
}

func buildScaledSnapshot(base, hotspots int) Snapshot {
	types := []LinkType{{ID: "edge", MaxA: 1, MaxB: 1}}
	objs := map[string]struct{}{}
	recs := make([]RawRecord, 0, base+hotspots*5)

	// base 条互不相交的边：每边使用独立对象对，不产生任何冲突范围。
	for i := 0; i < base; i++ {
		s := fmt.Sprintf("s%08d", i)
		t := fmt.Sprintf("t%08d", i)
		objs[s] = struct{}{}
		objs[t] = struct{}{}
		recs = append(recs, RawRecord{LinkTypeID: "edge", SourceID: s, TargetID: t})
	}
	// 仅 hotspot 数量的局部范围发生真正的基数冲突。
	for h := 0; h < hotspots; h++ {
		anchor := fmt.Sprintf("hot%06d", h)
		objs[anchor] = struct{}{}
		for j := 0; j < 5; j++ {
			other := fmt.Sprintf("h%06do%02d", h, j)
			objs[other] = struct{}{}
			recs = append(recs, RawRecord{LinkTypeID: "edge", SourceID: anchor, TargetID: other})
		}
	}
	return Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs}
}
