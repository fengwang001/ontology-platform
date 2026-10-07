package ontology

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// canonicalDump 把查询结果渲染为规范字符串，用于增量视图与朴素模型逐条对照。
func canonicalDump(groups []GroupView) string {
	s := ""
	for _, g := range groups {
		s += fmt.Sprintf("G%d:", g.Key)
		for _, m := range g.Members {
			s += fmt.Sprintf("[%s|%s|%d]", m.ObjectTypeID, m.ObjectID, m.InstantUnix)
		}
		s += "\n"
	}
	return s
}

func dumpHash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// auditRecord 是差分测试的一条审计记录：操作输入、所依据的时区版本与对照结论。
type auditRecord struct {
	Run       int    `json:"run"`
	OpSeq     int    `json:"opSeq"`
	Op        string `json:"op"`
	Input     string `json:"input"`
	TzVersion int    `json:"tzVersion,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
	ViewHash  uint64 `json:"viewHash,omitempty"`
	NaiveHash uint64 `json:"naiveHash,omitempty"`
}

func auditDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("ONTOLOGY_AUDIT_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "ontology-audit")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create audit dir: %v", err)
	}
	return dir
}

// TestDifferentialRandomOpSequences 在随机操作序列下把增量视图与独立实现的
// 朴素全量重建模型逐条对照；每次对照的输入、时区版本与结论写入 JSONL 审计日志。
func TestDifferentialRandomOpSequences(t *testing.T) {
	zones := []string{"UTC", "UTC+08:00", "UTC+05:30", "UTC-05:00", "UTC-08:00", "UTC+00:30"}
	types := []string{"A", "B"}
	dir := auditDir(t)

	for seed := int64(1); seed <= 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		p := NewPlatform()
		p.AddObjectType("A", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
		p.AddObjectType("B", "at", &TzDefVersion{Version: 1, ZoneID: "UTC+08:00", EffectiveSeq: 1})
		p.AddLink("a-b", "A", "B")
		p.CreateView("v", "a-b")

		auditPath := filepath.Join(dir, fmt.Sprintf("differential-seed%d.jsonl", seed))
		f, err := os.Create(auditPath)
		if err != nil {
			t.Fatalf("create audit file: %v", err)
		}
		enc := json.NewEncoder(f)
		logRec := func(rec auditRecord) {
			if err := enc.Encode(rec); err != nil {
				t.Fatalf("write audit: %v", err)
			}
		}

		lastVersion := map[string]int{"A": 1, "B": 1}
		lastEff := map[string]uint64{"A": 1, "B": 1}
		var maxSeq uint64
		base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		compares := 0

		for op := 0; op < 300; op++ {
			roll := rng.Intn(100)
			switch {
			case roll < 40: // 正常写入
				typ := types[rng.Intn(len(types))]
				obj := fmt.Sprintf("%s-%d", typ, rng.Intn(8))
				val := base.Add(time.Duration(rng.Intn(24*60)) * time.Hour)
				evt := p.WriteTimeProperty(typ, obj, val)
				maxSeq = evt.WriteSeq
				logRec(auditRecord{Run: int(seed), OpSeq: op, Op: "write",
					Input:     fmt.Sprintf("%s/%s seq=%d local=%s", typ, obj, evt.WriteSeq, val),
					TzVersion: evt.AnchoredTzVersion})

			case roll < 50: // 注入乱序/滞后/非法事件
				typ := types[rng.Intn(len(types))]
				obj := fmt.Sprintf("%s-%d", typ, rng.Intn(8))
				seq := uint64(rng.Intn(int(maxSeq) + 4))
				evt := ChangeEvent{
					ObjectID:     obj,
					ObjectTypeID: typ,
					WriteSeq:     seq,
					LocalValue:   base.Add(time.Duration(rng.Intn(24*60)) * time.Hour),
				}
				if r := rng.Intn(10); r > 0 { // 90% 锚定一个已存在的版本
					v := 1 + rng.Intn(lastVersion[typ])
					evt.AnchoredTzVersion = v
					evt.AnchoredZoneID = zones[rng.Intn(len(zones))]
				} // 10% 锚定版本为 0（写入时刻未定义默认时区）
				p.InjectEvent(evt)
				if evt.WriteSeq > maxSeq {
					maxSeq = evt.WriteSeq
				}
				logRec(auditRecord{Run: int(seed), OpSeq: op, Op: "inject",
					Input:     fmt.Sprintf("%s/%s seq=%d zone=%q", typ, obj, seq, evt.AnchoredZoneID),
					TzVersion: evt.AnchoredTzVersion})

			case roll < 60: // 合法迁移
				typ := types[rng.Intn(len(types))]
				tv := TzDefVersion{
					Version:      lastVersion[typ] + 1,
					ZoneID:       zones[rng.Intn(len(zones))],
					EffectiveSeq: lastEff[typ] + 1 + uint64(rng.Intn(5)),
				}
				err := p.MigrateDefaultTz(typ, tv)
				logRec(auditRecord{Run: int(seed), OpSeq: op, Op: "migrate",
					Input:     fmt.Sprintf("%s -> %+v err=%v", typ, tv, err),
					TzVersion: tv.Version})
				if err == nil {
					lastVersion[typ] = tv.Version
					lastEff[typ] = tv.EffectiveSeq
				}

			case roll < 65: // 非法迁移
				typ := types[rng.Intn(len(types))]
				tv := TzDefVersion{Version: lastVersion[typ], ZoneID: "Nowhere/Somewhere",
					EffectiveSeq: lastEff[typ]}
				err := p.MigrateDefaultTz(typ, tv)
				logRec(auditRecord{Run: int(seed), OpSeq: op, Op: "migrate-invalid",
					Input: fmt.Sprintf("%s -> %+v err=%v", typ, tv, err)})
				if err == nil {
					t.Fatalf("seed=%d op=%d: invalid migration accepted", seed, op)
				}

			case roll < 70: // 废弃分组属性
				typ := types[rng.Intn(len(types))]
				p.DeprecateGroupingProperty(typ)
				logRec(auditRecord{Run: int(seed), OpSeq: op, Op: "deprecate", Input: typ})

			case roll < 73: // 删除对象类型
				typ := types[rng.Intn(len(types))]
				p.DeleteObjectType(typ)
				logRec(auditRecord{Run: int(seed), OpSeq: op, Op: "delete-type", Input: typ})

			default: // 增量维护 + 与朴素模型对照
				res := p.Maintain("v")
				view := canonicalDump(mustQuery(p))
				naive := canonicalDump(p.NaiveDump("v"))
				vh, nh := dumpHash(view), dumpHash(naive)
				verdict := "equal"
				if view != naive {
					verdict = "MISMATCH"
				}
				logRec(auditRecord{Run: int(seed), OpSeq: op, Op: "maintain-compare",
					Input:   fmt.Sprintf("applied=%d stale=%d errors=%v", res.Applied, res.Stale, res.Errors),
					Verdict: verdict, ViewHash: vh, NaiveHash: nh})
				if view != naive {
					t.Fatalf("seed=%d op=%d: incremental vs naive diverged\nincremental:\n%s\nnaive:\n%s\naudit: %s",
						seed, op, view, naive, auditPath)
				}
				groups, _ := p.Query("v")
				checkViewInvariants(t, groups)
				compares++
			}
		}
		// 收尾：最终维护后再对照一次。
		p.Maintain("v")
		if view, naive := canonicalDump(mustQuery(p)), canonicalDump(p.NaiveDump("v")); view != naive {
			t.Fatalf("seed=%d final: diverged\nincremental:\n%s\nnaive:\n%s", seed, view, naive)
		}
		f.Close()
		t.Logf("seed=%d: %d comparisons passed, audit at %s", seed, compares, auditPath)
	}
}

func mustQuery(p *Platform) []GroupView {
	groups, _ := p.Query("v")
	return groups
}
