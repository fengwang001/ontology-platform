package ontology

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// 随机操作序列下，Rebuilder（带检查点）与独立朴素重放模型逐条对照；
// 每次判定的输入、所依据规则版本与对照结论均写入审计日志以便事后核查。
func TestDifferentialRandomSequences(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	typeIDs := []string{"A", "B", "C", "D", "Ghost"}
	props := []string{"score", "tag", "bOnly", "cOnly", "unknown"}

	compareCount := 0
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		schema := testSchema()
		store := NewEventStore()
		rb := NewRebuilder(store, schema, 1+rng.Intn(8))
		const id = "inst"

		clock := int64(0)
		nextTime := func() int64 {
			// 多数时候递增，偶尔并列或回退，以覆盖乱序与并列记录。
			switch r := rng.Intn(10); {
			case r < 7:
				clock += 1 + rng.Int63n(5)
			case r < 9:
				// 保持同一时刻（可能产生并列记录）
			default:
				clock -= rng.Int63n(3) // 回退，触发检查点失效
			}
			return clock
		}

		created := false
		lastEff := int64(0)
		for op := 0; op < 60; op++ {
			tm := nextTime()
			switch rng.Intn(10) {
			case 0, 1, 2, 3: // 属性赋值
				var v Value
				if rng.Intn(2) == 0 {
					v = IntVal(rng.Int63n(120))
				} else {
					v = StrVal([]string{"a", "b", "c", "x", "y", "z"}[rng.Intn(6)])
				}
				store.Append(set(id, tm, props[rng.Intn(len(props))], v))
			case 4, 5, 6: // 类型演变
				store.Append(evolve(id, tm, typeIDs[rng.Intn(len(typeIDs))]))
			case 7: // 创建（重复创建应被忽略）
				store.Append(create(id, tm, typeIDs[rng.Intn(3)]))
				created = true
			case 8: // 追加新规则版本（收紧 score，新增子类型 D）
				eff := clock + 1 + rng.Int63n(5)
				if eff <= lastEff {
					eff = lastEff + 1
				}
				lastEff = eff
				v := RuleVersion{
					EffectiveFrom: eff,
					Types: map[string]TypeDef{
						"A": {ID: "A", LocalProps: map[string]ValueRange{
							"score": {Kind: IntRangeKind, Min: 0, Max: 60},
							"tag":   {Kind: EnumRangeKind, Enum: []string{"a", "b"}},
						}},
						"B": {ID: "B", Parent: "A", Overrides: map[string]ValueRange{
							"score": {Kind: IntRangeKind, Min: 0, Max: 10},
						}},
						"D": {ID: "D", Parent: "A"},
					},
				}
				if err := schema.AppendVersion(v); err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
			default: // 什么都不做，拉长事件间隔
			}
			if !created && rng.Intn(5) == 0 {
				store.Append(create(id, nextTime(), "A"))
				created = true
			}

			// 随机截止时刻对照一次。
			cutoff := clock - rng.Int63n(10)
			got, stats, gerr := rb.Rebuild(id, cutoff)
			want, werr := NaiveRebuild(schema, store.Events(id), cutoff)

			match := errors.Is(gerr, werr) && (gerr != nil || reflect.DeepEqual(got, want))
			conclusion := "match"
			if !match {
				conclusion = "mismatch"
			}
			sv := 0
			if cutoff >= 0 {
				sv = schema.VersionAt(cutoff).ID
			}
			rec := AuditRecord{
				Kind:           "compare",
				InstanceID:     id,
				Cutoff:         cutoff,
				SchemaVersion:  sv,
				EventCount:     store.Len(),
				EventsScanned:  stats.EventsScanned,
				CheckpointUsed: stats.CheckpointUsed,
				ResultType:     got.TypeID,
				Conclusion:     conclusion,
			}
			if gerr != nil {
				rec.Err = gerr.Error()
			}
			logger.Log(rec)
			compareCount++

			if !match {
				t.Fatalf("seed %d op %d cutoff %d:\n got %+v err %v\nwant %+v err %v",
					seed, op, cutoff, got, gerr, want, werr)
			}
		}
	}
	if compareCount == 0 {
		t.Fatal("no comparisons performed")
	}
	t.Logf("comparisons: %d, audit: %s", compareCount, auditPath)

	// 核查审计日志：每条记录必须包含输入、规则版本与结论。
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var seen int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec AuditRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("corrupt audit record: %v", err)
		}
		if rec.Kind != "compare" || rec.Conclusion == "" || rec.InstanceID == "" {
			t.Fatalf("audit record missing required fields: %+v", rec)
		}
		if rec.Conclusion != "match" {
			t.Fatalf("audit recorded mismatch: %+v", rec)
		}
		seen++
	}
	if seen != compareCount {
		t.Fatalf("audit records %d != comparisons %d", seen, compareCount)
	}
}

// 审计日志也应记录单次重建判定（rebuild 类记录）。
func TestAuditRebuildRecord(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "rebuild.jsonl")
	logger, err := NewFileAuditLogger(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	schema := testSchema()
	store := NewEventStore()
	rb := NewRebuilder(store, schema, 2)
	const id = "inst"
	store.Append(create(id, 1, "A"))
	store.Append(set(id, 2, "score", IntVal(42)))

	st, stats, err := rb.Rebuild(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	logger.Log(AuditRecord{
		Kind:           "rebuild",
		InstanceID:     id,
		Cutoff:         2,
		SchemaVersion:  schema.VersionAt(2).ID,
		EventCount:     store.Len(),
		EventsScanned:  stats.EventsScanned,
		CheckpointUsed: stats.CheckpointUsed,
		ResultType:     st.TypeID,
		Conclusion:     fmt.Sprintf("values=%d", len(st.Values)),
	})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var rec AuditRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Kind != "rebuild" || rec.ResultType != "A" || rec.SchemaVersion != 0 {
		t.Fatalf("unexpected audit record: %+v", rec)
	}
}
