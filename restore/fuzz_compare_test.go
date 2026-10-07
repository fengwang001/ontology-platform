package restore

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// randomSnapshot 按固定随机源构造结构合法、但损坏组合随机的快照。
//
// 生成内容：T 个类型、O 个对象（随机挂类型）、L 条链接（随机两端，
// 可能指向不存在对象以产生悬空引用）、A 条动作（随机对象/链接依赖集合）。
// 每类独立掷骰：整体缺失 / 整体损坏 / 完好；逐条记录独立掷骰是否损坏。
// 以一定概率加入跨类或同类额外依赖边，制造潜在循环。
func randomSnapshot(rng *rand.Rand) *Snapshot {
	snap := &Snapshot{Classes: map[Class]ClassBackup{}}
	pickClass := func() {
		for _, c := range Classes() {
			cb := ClassBackup{}
			switch rng.Intn(10) {
			case 0:
				cb.Missing = true
			case 1:
				cb.CorruptAll = true
			}
			snap.Classes[c] = cb
		}
	}
	pickClass()

	tCount := 1 + rng.Intn(4)
	for i := 0; i < tCount; i++ {
		snap.Types = append(snap.Types, TypeDef{
			Key:   keyAt("t", i),
			State: maybeCorrupt(rng),
		})
	}
	typeKeys := make([]string, len(snap.Types))
	for i, t := range snap.Types {
		typeKeys[i] = t.Key
	}

	oCount := 1 + rng.Intn(6)
	for i := 0; i < oCount; i++ {
		snap.Objects = append(snap.Objects, ObjectInstance{
			Key:     keyAt("o", i),
			TypeKey: typeKeys[rng.Intn(len(typeKeys))],
			State:   maybeCorrupt(rng),
		})
	}
	objectKeys := append([]string(nil), keysOfObjects(snap.Objects)...)

	lCount := rng.Intn(5)
	for i := 0; i < lCount; i++ {
		src := pickRef(rng, objectKeys, 0.15)
		dst := pickRef(rng, objectKeys, 0.15)
		snap.Links = append(snap.Links, LinkInstance{
			Key:          keyAt("l", i),
			SourceObject: src,
			TargetObject: dst,
			State:        maybeCorrupt(rng),
		})
	}
	linkKeys := keysOfLinks(snap.Links)

	aCount := rng.Intn(5)
	for i := 0; i < aCount; i++ {
		act := ActionRecord{Key: keyAt("a", i), State: maybeCorrupt(rng)}
		for _, k := range objectKeys {
			if rng.Intn(2) == 0 {
				act.Objects = append(act.Objects, k)
			}
		}
		if rng.Float64() < 0.1 {
			act.Objects = append(act.Objects, "ghost-obj")
		}
		for _, k := range linkKeys {
			if rng.Intn(2) == 0 {
				act.Links = append(act.Links, k)
			}
		}
		snap.Actions = append(snap.Actions, act)
	}

	// 以约 12% 概率注入额外对象间依赖边，偶尔制造环。
	if len(objectKeys) >= 2 && rng.Intn(8) == 0 {
		i, j := rng.Intn(len(objectKeys)), rng.Intn(len(objectKeys))
		snap.ExtraDeps = append(snap.ExtraDeps, Edge{
			From: id(ClassObject, objectKeys[i]),
			To:   id(ClassObject, objectKeys[j]),
		})
		if rng.Intn(2) == 0 && i != j {
			snap.ExtraDeps = append(snap.ExtraDeps, Edge{
				From: id(ClassObject, objectKeys[j]),
				To:   id(ClassObject, objectKeys[i]),
			})
		}
	}
	return snap
}

func maybeCorrupt(rng *rand.Rand) RecordState {
	if rng.Intn(100) < 25 {
		return StateCorrupt
	}
	return StateIntact
}

func pickRef(rng *rand.Rand, keys []string, ghostProb float64) string {
	if rng.Float64() < ghostProb {
		return "missing-" + keyAt("x", rng.Intn(1000))
	}
	return keys[rng.Intn(len(keys))]
}

func keyAt(prefix string, i int) string {
	return prefix + "-" + padIndex(i)
}

func keysOfObjects(os []ObjectInstance) []string {
	keys := make([]string, len(os))
	for i, o := range os {
		keys[i] = o.Key
	}
	return keys
}

func keysOfLinks(ls []LinkInstance) []string {
	keys := make([]string, len(ls))
	for i, l := range ls {
		keys[i] = l.Key
	}
	return keys
}

// caseRecord 是落盘审计中的一次裁决记录。
type caseRecord struct {
	Index       int                   `json:"index"`
	Seed        int64                 `json:"seed"`
	Input       *Snapshot             `json:"input"`
	ClassStatus map[Class]ClassStatus `json:"class_status"`
	Recoverable map[string]bool       `json:"recoverable"`
	Reasons     map[string]string     `json:"reasons"`
	Plan        []string              `json:"plan"`
	ErrorCodes  []int                 `json:"error_codes"`
	AgreesNaive bool                  `json:"agrees_naive"`
}

// TestFuzzAgainstNaiveReference 在大量随机构造的损坏组合上，
// 将正式裁决与朴素参照模型逐案对照，并记录每次裁决的输入、输出与判定依据。
func TestFuzzAgainstNaiveReference(t *testing.T) {
	const cases = 400
	dir := filepath.Join("..", "testdata", "fuzz")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	out, err := os.Create(filepath.Join(dir, "cases.jsonl"))
	if err != nil {
		t.Fatalf("create audit file: %v", err)
	}
	defer out.Close()
	enc := json.NewEncoder(out)

	for i := 0; i < cases; i++ {
		seed := int64(20261007 + i)
		rng := rand.New(rand.NewSource(seed))
		snap := randomSnapshot(rng)

		v := New().Adjudicate(snap)
		nv := naiveAdjudicate(snap)
		ok, mismatches := nv.agreesWith(v)

		rec := caseRecord{
			Index:       i,
			Seed:        seed,
			Input:       snap,
			ClassStatus: v.ClassStatuses,
			Recoverable: map[string]bool{},
			Reasons:     map[string]string{},
			AgreesNaive: ok,
		}
		for _, st := range v.Plan {
			if st.Kind == StepBarrier {
				rec.Plan = append(rec.Plan, "barrier:"+st.CompletedClass.String())
			} else {
				rec.Plan = append(rec.Plan, st.Record.String())
			}
		}
		for rid, rv := range v.Records {
			rec.Recoverable[rid.String()] = rv.Recoverable
			rec.Reasons[rid.String()] = rv.Detail
		}
		for _, e := range v.Errors {
			rec.ErrorCodes = append(rec.ErrorCodes, int(e.Code))
		}
		if err := enc.Encode(rec); err != nil {
			t.Fatalf("write case %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("case %d seed %d mismatch with naive model at %v", i, seed, mismatches)
		}
	}
}
