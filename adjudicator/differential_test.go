package adjudicator

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// naiveRebuildable 为朴素参照模型：对每条记录独立地沿依赖关系
// 逐步递归判定，不做任何分量合并或排序优化。
// 随机用例只生成无环依赖图，因此无需处理循环。
func naiveRebuildable(s *Snapshot, r RecordRef, memo map[RecordRef]bool) bool {
	if v, ok := memo[r]; ok {
		return v
	}
	b := s.Backups[r.Category]
	ok := b.Available() && !b.Damaged[r.ID]
	var rec *Record
	if ok {
		for i := range b.Records {
			if b.Records[i].Ref == r {
				rec = &b.Records[i]
				break
			}
		}
		ok = rec != nil
	}
	if ok {
		for _, dep := range rec.DependsOn {
			if !naiveRebuildable(s, dep, memo) {
				ok = false
				break
			}
		}
	}
	memo[r] = ok
	return ok
}

// randomSnapshot 按给定种子随机构造一份备份快照：
// 每个类别随机处于 整体缺失/整体损坏/部分损坏/完好 之一，
// 依赖边随机但保证无环（只指向类别优先级不更高、且同类中编号更小的记录），
// 并随机混入指向不存在记录的悬空依赖。
func randomSnapshot(rng *rand.Rand) *Snapshot {
	s := &Snapshot{}
	ids := [categoryCount][]string{}
	for c := 0; c < categoryCount; c++ {
		n := rng.Intn(6)
		for i := 0; i < n; i++ {
			ids[c] = append(ids[c], fmt.Sprintf("c%dr%d", c, i))
		}
		state := rng.Intn(4) // 0=缺失 1=整体损坏 2=部分损坏 3=完好
		b := CategoryBackup{Present: state != 0, WhollyCorrupt: state == 1}
		if state == 2 {
			b.Damaged = map[string]bool{}
			for _, id := range ids[c] {
				if rng.Intn(2) == 0 {
					b.Damaged[id] = true
				}
			}
		}
		for i, id := range ids[c] {
			rec := Record{Ref: RecordRef{Category: Category(c), ID: id}}
			for d := 0; d < rng.Intn(3); d++ {
				depCat := rng.Intn(c + 1)
				switch {
				case rng.Intn(8) == 0:
					// 悬空依赖：指向备份中不存在的记录。
					rec.DependsOn = append(rec.DependsOn,
						RecordRef{Category: Category(depCat), ID: "ghost"})
				case depCat == c && i > 0:
					j := rng.Intn(i)
					rec.DependsOn = append(rec.DependsOn,
						RecordRef{Category: Category(c), ID: ids[c][j]})
				case depCat < c && len(ids[depCat]) > 0:
					j := rng.Intn(len(ids[depCat]))
					rec.DependsOn = append(rec.DependsOn,
						RecordRef{Category: Category(depCat), ID: ids[depCat][j]})
				}
			}
			b.Records = append(b.Records, rec)
		}
		s.Backups[c] = b
	}
	return s
}

// differentialEntry 为一次对照裁决的完整记录：输入、输出与判定依据。
type differentialEntry struct {
	Seed     int64           `json:"seed"`
	Input    *Snapshot       `json:"input"`
	Order    []RecordRef     `json:"order"`
	Verdicts []RecordVerdict `json:"verdicts"`
	Log      []DecisionEntry `json:"log"`
	Err      *Error          `json:"err,omitempty"`
}

// 在大量随机构造的损坏组合上，与朴素参照模型对照：
// 每条记录的可重建性必须一致，重建顺序必须合法且重复裁决一致，
// 并把每次裁决的输入、输出与判定依据写入 JSONL 日志文件。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	logPath := filepath.Join("testdata", "differential_log.jsonl")
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	enc := json.NewEncoder(logFile)

	const cases = 500
	for seed := int64(0); seed < cases; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := randomSnapshot(rng)
		v := New().Adjudicate(s)

		// 1. 每条记录的可重建性与朴素参照模型一致。
		memo := map[RecordRef]bool{}
		for _, rv := range v.Verdicts {
			want := naiveRebuildable(s, rv.Ref, memo)
			if rv.Rebuildable != want {
				t.Fatalf("seed=%d 记录 %v: 裁决=%v 参照=%v", seed, rv.Ref, rv.Rebuildable, want)
			}
		}
		// 2. 每条记录都有判定依据。
		if len(v.Log) != len(v.Verdicts) {
			t.Fatalf("seed=%d 判定依据条数 %d 与判定条数 %d 不一致", seed, len(v.Log), len(v.Verdicts))
		}
		for _, entry := range v.Log {
			if entry.Detail == "" {
				t.Fatalf("seed=%d 记录 %v 缺少判定理由", seed, entry.Ref)
			}
		}
		// 3. 重建顺序合法（依赖先于被依赖）。
		assertOrderValid(t, s, v)
		// 4. 重复裁决结果完全一致。
		again := New().Adjudicate(s)
		if !reflect.DeepEqual(v, again) {
			t.Fatalf("seed=%d 重复裁决结果不一致", seed)
		}
		// 5. 记录本次裁决的输入、输出与判定依据。
		if err := enc.Encode(differentialEntry{
			Seed: seed, Input: s, Order: v.Order,
			Verdicts: v.Verdicts, Log: v.Log, Err: v.Err,
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("已写入 %d 条对照裁决日志: %s", cases, logPath)
}
