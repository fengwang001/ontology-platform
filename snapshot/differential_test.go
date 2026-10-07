package snapshot

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// issueTuple 是跨实现比较用的最小事实：类别 + 位置 + 引用目标。
// 刻意不比较错误文案，只比较“判定了什么、命中哪个块”。
type issueTuple struct {
	kind       IssueKind
	typ        string
	chunk      int
	targetType string
	targetID   string
}

func tuplesFromMain(issues []Issue) map[issueTuple]int {
	m := map[issueTuple]int{}
	for _, is := range issues {
		tup := issueTuple{kind: is.Kind, typ: is.Chunk.Type, chunk: is.Chunk.Chunk}
		if is.Ref != nil {
			tup.targetType = is.Ref.TargetType
			tup.targetID = is.Ref.TargetID
		}
		m[tup]++
	}
	return m
}

func tuplesFromNaive(issues []naiveIssue) map[issueTuple]int {
	m := map[issueTuple]int{}
	for _, is := range issues {
		m[issueTuple{
			kind: is.Category, typ: is.Type, chunk: is.Index,
			targetType: is.TargetType, targetID: is.TargetID,
		}]++
	}
	return m
}

func statusesFromNaive(reports map[ChunkRef]*naiveReport) map[string]string {
	out := map[string]string{}
	for ref, rep := range reports {
		switch {
		case !rep.OK:
			out[fmt.Sprintf("%s/%d", ref.Type, ref.Chunk)] = string(StatusIntegrityBad)
		case rep.CountBad:
			out[fmt.Sprintf("%s/%d", ref.Type, ref.Chunk)] = string(StatusCountMismatch)
		default:
			out[fmt.Sprintf("%s/%d", ref.Type, ref.Chunk)] = string(StatusTrusted)
		}
	}
	return out
}

// damageKind 是随机构造的损坏组合。
type damageKind int

const (
	damageNone damageKind = iota
	damageTamperRecord
	damageCorruptJSON
	damageDeclaredCount
	damageTruncate
	damageAppend
)

func applyDamage(t *testing.T, dir, typ string, idx int, dmg damageKind, rng *rand.Rand) {
	t.Helper()
	path := dir + "/" + chunkFileName(typ, idx)
	switch dmg {
	case damageNone:
	case damageCorruptJSON:
		if err := os.WriteFile(path, []byte("{broken-json"), 0o644); err != nil {
			t.Fatal(err)
		}
	default:
		env := loadChunkFile(t, dir, typ, idx)
		switch dmg {
		case damageTamperRecord:
			if len(env.Records) > 0 {
				pick := rng.Intn(len(env.Records))
				env.Records[pick].Data = []byte(fmt.Sprintf("corrupt-%d", rng.Int63()))
			}
		case damageDeclaredCount:
			env.Header.DeclaredCount += 1 + rng.Intn(3)
		case damageTruncate:
			if len(env.Records) <= 1 {
				return // 无法截断：退化为仅改声明条数
			}
			env.Records = env.Records[:len(env.Records)-1]
			sum, err := computeChecksum(env.Records)
			if err != nil {
				t.Fatal(err)
			}
			env.Checksum = sum
		case damageAppend:
			env.Records = append(env.Records, Record{ID: fmt.Sprintf("extra-%d", rng.Int63())})
			sum, err := computeChecksum(env.Records)
			if err != nil {
				t.Fatal(err)
			}
			env.Checksum = sum
		}
		writeChunkFile(t, dir, typ, idx, env)
	}
}

// 10. 与独立朴素校验模型对照：随机构造损坏组合，结论必须逐类一致。
func TestRandomDifferential(t *testing.T) {
	iterations := 300
	if testing.Short() {
		iterations = 40
	}
	rng := rand.New(rand.NewSource(20261007))

	for iter := 0; iter < iterations; iter++ {
		dir := t.TempDir()
		typeNames := []string{"T0", "T1", "T2", "T3"}

		req := ExportRequest{Chunks: map[string][][]Record{}}
		type chunkPos struct {
			typ string
			idx int
		}
		var positions []chunkPos
		for _, typ := range typeNames {
			nChunks := 1 + rng.Intn(2)
			for c := 0; c < nChunks; c++ {
				nRecs := 1 + rng.Intn(3)
				var recs []Record
				for r := 0; r < nRecs; r++ {
					rec := Record{ID: fmt.Sprintf("%s-%d-%d", typ, c, r)}
					if rng.Intn(2) == 0 {
						target := typeNames[rng.Intn(len(typeNames))]
						tid := fmt.Sprintf("%s-%d-%d", target, rng.Intn(2), rng.Intn(3))
						if rng.Intn(2) == 0 {
							tid = fmt.Sprintf("dangling-%d", rng.Int63()%100)
						}
						rec.Refs = []CrossTypeRef{{
							Field: "f", TargetType: target, TargetID: tid,
						}}
					}
					recs = append(recs, rec)
				}
				req.Chunks[typ] = append(req.Chunks[typ], recs)
				positions = append(positions, chunkPos{typ, c})
			}
		}
		mustExport(t, dir, req)

		// 随机损坏组合。
		damageByPos := map[chunkPos]damageKind{}
		for _, p := range positions {
			if rng.Intn(100) < 45 {
				dmg := damageKind(1 + rng.Intn(int(damageAppend)))
				applyDamage(t, dir, p.typ, p.idx, dmg, rng)
				damageByPos[p] = dmg
			}
		}

		// 随机请求集合，可能包含范围外类型。
		var requested []string
		for _, typ := range typeNames {
			if rng.Intn(2) == 0 {
				requested = append(requested, typ)
			}
		}
		if rng.Intn(3) == 0 {
			requested = append(requested, fmt.Sprintf("Outside%d", rng.Intn(3)))
		}
		if len(requested) == 0 {
			requested = []string{"T0"}
		}

		mainRes, err := NewLoader(dir).Load(requested, nil)
		if err != nil {
			t.Fatalf("iter=%d main load err=%v", iter, err)
		}
		mainAgg, err := NewLoader(dir).Aggregate(requested, nil)
		if err != nil {
			t.Fatalf("iter=%d main agg err=%v", iter, err)
		}
		naiveRes, err := naiveVerify(dir, requested)
		if err != nil {
			t.Fatalf("iter=%d naive err=%v", iter, err)
		}

		mainTuples := tuplesFromMain(mainRes.Issues)
		naiveTuples := tuplesFromNaive(naiveRes.Issues)
		if !mapsEqualCount(mainTuples, naiveTuples) {
			t.Fatalf("iter=%d damage=%v requested=%v\nmain=%v\nnaive=%v",
				iter, damageByPos, requested, mainTuples, naiveTuples)
		}

		// 块级状态逐块一致。
		naiveStatus := statusesFromNaive(naiveRes.Reports)
		for ref, rep := range mainRes.Reports {
			key := fmt.Sprintf("%s/%d", ref.Type, ref.Chunk)
			if naiveStatus[key] != string(rep.Status) {
				t.Fatalf("iter=%d chunk %s: main=%s naive=%s",
					iter, key, rep.Status, naiveStatus[key])
			}
		}

		if mainAgg.Aggregatable != naiveRes.Aggregatable {
			t.Fatalf("iter=%d aggregatable: main=%v naive=%v",
				iter, mainAgg.Aggregatable, naiveRes.Aggregatable)
		}
	}
}

func mapsEqualCount(a, b map[issueTuple]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
