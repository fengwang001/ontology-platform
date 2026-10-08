package ontology_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"

	"ontology"
	"ontology/naive"
)

// diffLogEntry 记录一次差分查询的输入、输出与达成结果所依据的候选路径。
type diffLogEntry struct {
	Seed   int                 `json:"seed"`
	Query  ontology.PathQuery  `json:"query"`
	Result ontology.PathResult `json:"result"`
	Err    string              `json:"error,omitempty"`
}

// TestDifferentialAgainstNaive 在随机生成的对象与链接序列上，将主引擎与
// 独立实现的朴素穷举模型逐一对照。每次查询的输入、输出与候选路径通过
// t.Log 记录；设置环境变量 ONTOLOGY_DIFF_LOG 可额外写出 JSONL 日志。
func TestDifferentialAgainstNaive(t *testing.T) {
	var logFile *os.File
	if path := os.Getenv("ONTOLOGY_DIFF_LOG"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create diff log: %v", err)
		}
		defer f.Close()
		logFile = f
	}

	categories := []ontology.Category{"c0", "c1", "c2"}
	principals := []ontology.Principal{"p0", "p1", "p2"}
	seeds := 300
	if v := os.Getenv("ONTOLOGY_DIFF_SEEDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			seeds = n
		}
	}
	maxObj := 6
	maxLinks := 15
	if v := os.Getenv("ONTOLOGY_DIFF_MAXOBJ"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxObj = n
		}
	}
	if v := os.Getenv("ONTOLOGY_DIFF_MAXLINKS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxLinks = n
		}
	}

	for seed := 0; seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		g, err := ontology.NewGraph([]ontology.ObjectTypeSpec{
			{ID: "T0"},
			{ID: "T1"},
			{ID: "TF", ForbiddenInPathQuery: true},
		}, categories)
		if err != nil {
			t.Fatal(err)
		}

		// 随机对象实例。
		numObj := 3 + rng.Intn(maxObj)
		objects := make([]ontology.ObjectID, numObj)
		objType := make(map[ontology.ObjectID]ontology.ObjectTypeID, numObj)
		for i := range objects {
			objects[i] = ontology.ObjectID(fmt.Sprintf("o%d", i))
			typ := ontology.ObjectTypeID(fmt.Sprintf("T%d", rng.Intn(2)))
			if rng.Intn(10) == 0 {
				typ = "TF"
			}
			objType[objects[i]] = typ
			if err := g.AddObject(objects[i], typ); err != nil {
				t.Fatal(err)
			}
		}

		// 随机链接类型。
		numLT := 1 + rng.Intn(4)
		linkTypes := make([]ontology.LinkTypeID, numLT)
		linkSpecs := make(map[ontology.LinkTypeID]ontology.LinkTypeSpec, numLT)
		for i := range linkTypes {
			linkTypes[i] = ontology.LinkTypeID(fmt.Sprintf("lt%d", i))
			spec := ontology.LinkTypeSpec{
				ID:            linkTypes[i],
				From:          ontology.ObjectTypeID(fmt.Sprintf("T%d", rng.Intn(2))),
				To:            ontology.ObjectTypeID(fmt.Sprintf("T%d", rng.Intn(2))),
				Bidirectional: rng.Intn(2) == 0,
				Category:      categories[rng.Intn(len(categories))],
				Cost:          int64(rng.Intn(5)),
			}
			if rng.Intn(10) < 3 {
				// 限制为随机非空主体子集可见。
				for _, p := range principals[:1+rng.Intn(2)] {
					if rng.Intn(2) == 0 {
						spec.RestrictedTo = append(spec.RestrictedTo, p)
					}
				}
				if len(spec.RestrictedTo) == 0 {
					spec.RestrictedTo = []ontology.Principal{"p0"}
				}
			}
			if err := g.RegisterLinkType(spec); err != nil {
				t.Fatal(err)
			}
			linkSpecs[linkTypes[i]] = spec
		}

		// 随机链接实例（允许同对象对多条、允许重复同类型）。
		numLinks := rng.Intn(maxLinks)
		for i := 0; i < numLinks; i++ {
			lt := linkTypes[rng.Intn(numLT)]
			from, to, ok := pickEndpoints(rng, objects, objType, linkSpecs[lt])
			if !ok {
				continue
			}
			id := ontology.LinkID(fmt.Sprintf("l%d", i))
			if err := g.AddLink(id, lt, from, to); err != nil {
				t.Fatalf("seed %d: AddLink(%s, %s, %s, %s): %v", seed, id, lt, from, to, err)
			}
		}

		// 随机隔离标记。
		for _, o := range objects {
			if rng.Intn(7) == 0 {
				if err := g.SetIsolation(o, true); err != nil {
					t.Fatal(err)
				}
			}
		}

		snap := g.Snapshot()
		for qi := 0; qi < 2; qi++ {
			q := randomQuery(rng, objects, categories, principals)
			got, err1 := g.Query(q)
			want, err2 := naive.Solve(snap, q)

			entry := diffLogEntry{Seed: seed, Query: q, Result: got}
			if err1 != nil {
				entry.Err = err1.Error()
			}
			line, _ := json.Marshal(entry)
			t.Logf("seed=%d query=%+v result=%+v err=%v", seed, q, got, err1)
			if logFile != nil {
				fmt.Fprintf(logFile, "%s\n", line)
			}

			if !sameErrorKind(err1, err2) {
				t.Fatalf("seed %d query %+v: engine err=%v, naive err=%v", seed, q, err1, err2)
			}
			if err1 != nil {
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d query %+v:\nengine=%+v\nnaive =%+v", seed, q, got, want)
			}
		}
	}
}

// randomQuery 生成随机查询，偶尔注入非法参数以对照错误判定。
func randomQuery(rng *rand.Rand, objects []ontology.ObjectID, categories []ontology.Category, principals []ontology.Principal) ontology.PathQuery {
	q := ontology.PathQuery{
		Start:     objects[rng.Intn(len(objects))],
		End:       objects[rng.Intn(len(objects))],
		Principal: principals[rng.Intn(len(principals))],
	}
	if rng.Intn(10) == 0 {
		q.End = q.Start
	}

	switch r := rng.Intn(20); {
	case r == 0:
		// 非法：空约束序列。
		return q
	case r == 1:
		// 非法：中间位置带重复标记。
		return ontology.PathQuery{
			Start: q.Start, End: q.End, Principal: q.Principal,
			Pattern: []ontology.PatternElem{randomElem(rng, categories, false), randomElem(rng, categories, true), randomElem(rng, categories, false)},
		}
	}

	n := 1 + rng.Intn(3)
	q.Pattern = make([]ontology.PatternElem, n)
	for i := range q.Pattern {
		star := false
		if i == 0 && rng.Intn(5) < 2 {
			star = true
		}
		if i == n-1 && rng.Intn(5) < 2 {
			star = true
		}
		q.Pattern[i] = randomElem(rng, categories, star)
	}
	return q
}

func randomElem(rng *rand.Rand, categories []ontology.Category, star bool) ontology.PatternElem {
	if rng.Intn(5) < 2 {
		return ontology.PatternElem{Any: true, Star: star}
	}
	return ontology.PatternElem{Category: categories[rng.Intn(len(categories))], Star: star}
}

// pickEndpoints 为链接类型随机选择一对类型相容的端点；找不到时返回 false。
func pickEndpoints(rng *rand.Rand, objects []ontology.ObjectID, objType map[ontology.ObjectID]ontology.ObjectTypeID, spec ontology.LinkTypeSpec) (ontology.ObjectID, ontology.ObjectID, bool) {
	compatible := func(from, to ontology.ObjectID) bool {
		direct := objType[from] == spec.From && objType[to] == spec.To
		swapped := spec.Bidirectional && objType[from] == spec.To && objType[to] == spec.From
		return direct || swapped
	}
	for attempt := 0; attempt < 32; attempt++ {
		from := objects[rng.Intn(len(objects))]
		to := objects[rng.Intn(len(objects))]
		if rng.Intn(10) > 0 {
			for from == to {
				to = objects[rng.Intn(len(objects))]
			}
		}
		if compatible(from, to) {
			return from, to, true
		}
	}
	return "", "", false
}

// sameErrorKind 判定两个错误是否属于同一判定类别。
func sameErrorKind(a, b error) bool {
	kind := func(err error) int {
		switch {
		case err == nil:
			return 0
		case errors.Is(err, ontology.ErrInvalidParams):
			return 1
		case errors.Is(err, ontology.ErrForbiddenObjectType):
			return 2
		}
		return 3
	}
	return kind(a) == kind(b)
}
