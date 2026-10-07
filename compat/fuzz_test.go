package compat

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
)

var (
	fuzzObjTypes = []string{"Order", "Item", "Customer"}
	fuzzProps    = []string{"pa", "pb", "pc", "pd", "pe"}
	fuzzTypePool = []TypeSpec{
		strType(),
		{Kind: KindBoolean},
		intType(nil, nil),
		intType(FloatPtr(0), FloatPtr(100)),
		intType(FloatPtr(0), FloatPtr(10)),
		intType(FloatPtr(-50), FloatPtr(50)),
		{Kind: KindFloat},
		{Kind: KindFloat, Min: FloatPtr(0), Max: FloatPtr(1)},
		{Kind: KindEnum, Enum: []string{"a"}},
		{Kind: KindEnum, Enum: []string{"a", "b"}},
		{Kind: KindEnum, Enum: []string{"a", "b", "c"}},
	}
)

func randType(rng *rand.Rand) TypeSpec { return fuzzTypePool[rng.IntN(len(fuzzTypePool))] }

// genChain 随机生成一条版本演化链：2~5 个版本，每版 0~3 条变更。
func genChain(rng *rand.Rand) []Format {
	n := 2 + rng.IntN(4)
	versions := make([]Version, n)
	for i := range versions {
		versions[i] = v(i+1, 0, 0)
	}

	// 创世 Schema。
	schema := Schema{}
	for _, ot := range fuzzObjTypes[:1+rng.IntN(len(fuzzObjTypes))] {
		obj := ObjectType{}
		for _, p := range fuzzProps {
			if rng.IntN(2) == 0 {
				obj[p] = Property{Type: randType(rng), Required: rng.IntN(3) == 0}
			}
		}
		schema[ot] = obj
	}
	genesis := cloneSchema(schema)

	changes := make([][]Change, 0, n-1)
	for i := 1; i < n; i++ {
		var cs []Change
		for k := 0; k < rng.IntN(4); k++ {
			if c, ok := genChange(rng, schema); ok {
				cs = append(cs, c)
				schema = applyToSchema(schema, []Change{c})
			}
		}
		changes = append(changes, cs)
	}
	return buildChain(genesis, versions, changes)
}

// genChange 生成一条与当前 Schema 一致的变更；无可行变更时返回 ok=false。
// 绝不产生“无效果”的变更，保证变更日志与物化 Schema 严格互逆。
func genChange(rng *rand.Rand, s Schema) (Change, bool) {
	// 收集现有属性与可新增的位置。
	type slot struct{ obj, prop string }
	var existing []slot
	for obj, ot := range s {
		for p := range ot {
			existing = append(existing, slot{obj, p})
		}
	}
	var additions []slot
	for _, obj := range fuzzObjTypes {
		for _, p := range fuzzProps {
			if _, ok := lookupProp(s, obj, p); !ok {
				additions = append(additions, slot{obj, p})
			}
		}
	}

	op := rng.IntN(5)
	switch {
	case op == 0 && len(additions) > 0: // 新增
		a := additions[rng.IntN(len(additions))]
		return Change{ObjectType: a.obj, Property: a.prop, Kind: ChangeAddProperty,
			Type: randType(rng), Required: rng.IntN(3) == 0}, true
	case op == 1 && len(existing) > 0: // 删除
		e := existing[rng.IntN(len(existing))]
		p, _ := lookupProp(s, e.obj, e.prop)
		return Change{ObjectType: e.obj, Property: e.prop, Kind: ChangeRemoveProperty,
			Type: p.Type, Required: p.Required}, true
	case op == 2 && len(existing) > 0: // 改类型
		e := existing[rng.IntN(len(existing))]
		p, _ := lookupProp(s, e.obj, e.prop)
		return Change{ObjectType: e.obj, Property: e.prop, Kind: ChangeRetypeProperty,
			OldType: p.Type, Type: randType(rng)}, true
	case op == 3: // 改为必填
		var cands []slot
		for _, e := range existing {
			if p, _ := lookupProp(s, e.obj, e.prop); !p.Required {
				cands = append(cands, e)
			}
		}
		if len(cands) > 0 {
			e := cands[rng.IntN(len(cands))]
			return Change{ObjectType: e.obj, Property: e.prop, Kind: ChangeRequireProperty}, true
		}
	case op == 4: // 改为非必填
		var cands []slot
		for _, e := range existing {
			if p, _ := lookupProp(s, e.obj, e.prop); p.Required {
				cands = append(cands, e)
			}
		}
		if len(cands) > 0 {
			e := cands[rng.IntN(len(cands))]
			return Change{ObjectType: e.obj, Property: e.prop, Kind: ChangeUnrequireProperty}, true
		}
	}
	return Change{}, false
}

func valueFor(rng *rand.Rand, ts TypeSpec) any {
	switch ts.Kind {
	case KindString:
		return fmt.Sprintf("s%d", rng.IntN(5))
	case KindBoolean:
		return rng.IntN(2) == 0
	case KindInteger:
		lo, hi := -10, 20
		if ts.Min != nil {
			lo = int(*ts.Min)
		}
		if ts.Max != nil {
			hi = int(*ts.Max)
		}
		return lo + rng.IntN(hi-lo+1)
	case KindFloat:
		return rng.Float64() * 2
	case KindEnum:
		return ts.Enum[rng.IntN(len(ts.Enum))]
	}
	return nil
}

func junkValue(rng *rand.Rand) any {
	junk := []any{"junk", 9999, -9999, 3.14, true, "zz", 500.5}
	return junk[rng.IntN(len(junk))]
}

// genSnapshot 按快照版本的 Schema 生成数据，并注入偶发违规：
// 缺失必填属性、越界取值、已删除属性仍出现取值。
func genSnapshot(rng *rand.Rand, chain []Format, version Version) Snapshot {
	schema := chain[indexOfVersion(chain, version)].Schema
	snap := Snapshot{Version: version, Data: map[string][]map[string]any{}}
	for obj, ot := range schema {
		var objs []map[string]any
		for i := 0; i < rng.IntN(4); i++ {
			o := map[string]any{}
			for p, prop := range ot {
				present := rng.IntN(10) < 6
				if prop.Required {
					present = rng.IntN(10) < 9
				}
				if !present {
					continue
				}
				if rng.IntN(10) < 8 {
					o[p] = valueFor(rng, prop.Type)
				} else {
					o[p] = junkValue(rng)
				}
			}
			objs = append(objs, o)
		}
		snap.Data[obj] = objs
	}
	// 注入“已删除属性仍出现取值”：选取在链中某版本存在、但快照版本不存在的属性。
	if rng.IntN(10) < 3 {
		for obj, objs := range snap.Data {
			for _, p := range fuzzProps {
				if _, ok := schema[obj][p]; ok {
					continue
				}
				existsSomewhere := false
				for _, f := range chain {
					if _, ok := lookupProp(f.Schema, obj, p); ok {
						existsSomewhere = true
					}
				}
				if existsSomewhere && len(objs) > 0 {
					objs[rng.IntN(len(objs))][p] = junkValue(rng)
				}
			}
		}
	}
	return snap
}

func genProfile(rng *rand.Rand, chain []Format) Profile {
	n := len(chain)
	at := chain[rng.IntN(n)].Version
	lo := 1 + rng.IntN(n)
	hi := lo + rng.IntN(n+1-lo)
	p := Profile{
		Name:    fmt.Sprintf("consumer-%d", rng.IntN(100)),
		At:      at,
		Accepts: Range{Min: v(lo, 0, 0), Max: v(hi, 0, 0)},
		Mode:    Mode(rng.IntN(2)),
	}
	if rng.IntN(2) == 0 {
		p.Independent = map[string]map[string]bool{}
		for _, obj := range fuzzObjTypes {
			for _, prop := range fuzzProps {
				if rng.IntN(10) == 0 {
					if p.Independent[obj] == nil {
						p.Independent[obj] = map[string]bool{}
					}
					p.Independent[obj][prop] = true
				}
			}
		}
	}
	return p
}

// zeroStats 清零工作量统计，便于只对照判定结论与依据。
func zeroStats(cv ChainVerdict) ChainVerdict {
	for i := range cv.Steps {
		cv.Steps[i].Verdict.Stats = Stats{}
	}
	cv.Final.Stats = Stats{}
	return cv
}

type fuzzRecord struct {
	Seed        int64        `json:"seed"`
	Profile     Profile      `json:"profile"`
	SnapVersion Version      `json:"snap_version"`
	Chain       []Format     `json:"chain"`
	Snapshot    Snapshot     `json:"snapshot"`
	Optimized   ChainVerdict `json:"optimized"`
	Naive       ChainVerdict `json:"naive"`
	Match       bool         `json:"match"`
}

func runOneCase(seed int64) (fuzzRecord, error) {
	rng := rand.New(rand.NewPCG(0, uint64(seed)))
	chain := genChain(rng)
	snapVersion := chain[rng.IntN(len(chain))].Version
	snap := genSnapshot(rng, chain, snapVersion)
	profile := genProfile(rng, chain)

	got := zeroStats(JudgePath(profile, chain, snap))
	want := zeroStats(NaiveJudgePath(profile, chain, snap))
	rec := fuzzRecord{
		Seed: seed, Profile: profile, SnapVersion: snapVersion,
		Chain: chain, Snapshot: snap, Optimized: got, Naive: want,
		Match: reflect.DeepEqual(got, want),
	}
	if !rec.Match {
		return rec, fmt.Errorf("seed %d: optimized %+v != naive %+v", seed, got, want)
	}
	return rec, nil
}

// TestAgainstNaiveModel 在大量随机版本演化序列上，将优化实现与
// 按规则逐条独立判定的朴素参照模型对照；设置 COMPAT_FUZZ_LOG 时
// 把每次判定的输入、输出与依据记录为 JSONL。
func TestAgainstNaiveModel(t *testing.T) {
	var logFile *os.File
	if path := os.Getenv("COMPAT_FUZZ_LOG"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		logFile = f
	}
	enc := json.NewEncoder(logFile)

	const cases = 3000
	mismatches := 0
	for seed := int64(0); seed < cases; seed++ {
		rec, err := runOneCase(seed)
		if logFile != nil {
			if enc.Encode(rec) != nil {
				t.Fatalf("failed to write fuzz log")
			}
		}
		if err != nil {
			mismatches++
			t.Error(err)
			if mismatches > 5 {
				t.Fatalf("too many mismatches, stopping")
			}
		}
	}
}

// FuzzJudgePathAgainstNaive 是同一对照的 fuzz 入口。
func FuzzJudgePathAgainstNaive(f *testing.F) {
	for _, seed := range []int64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		if _, err := runOneCase(seed); err != nil {
			t.Error(err)
		}
	})
}
