package ontology

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

type callRecord struct {
	Iteration  int             `json:"iteration"`
	Caller     string          `json:"caller"`
	Objects    []string        `json:"objects"`
	Links      []naiveLinkJSON `json:"links"`
	Exist      []string        `json:"exist_permission"`
	Traverse   []string        `json:"traverse_permission"`
	HasCycle   bool            `json:"has_cycle"`
	Evidence   []string        `json:"evidence"`
	NaiveCycle bool            `json:"naive_has_cycle"`
	NaiveBest  []string        `json:"naive_evidence"`
}

type naiveLinkJSON struct {
	ID    string `json:"id"`
	From  string `json:"from"`
	To    string `json:"to"`
	Bidir bool   `json:"bidir"`
}

func TestRandomDifferentialAgainstNaive(t *testing.T) {
	const iterations = 600
	dir := filepath.Join("..", "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "hascycle-call-log.jsonl")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	enc := json.NewEncoder(logFile)

	for iter := 0; iter < iterations; iter++ {
		r := rand.New(rand.NewSource(int64(iter*7919 + 13)))
		n := 1 + r.Intn(6)
		ids := make([]string, n)
		for i := range ids {
			ids[i] = idFor(i)
		}

		g := newTestGraph(t)
		addObjs(t, g, ids...)

		sc := naiveScenario{
			objects:  append([]string(nil), ids...),
			exist:    map[string]bool{},
			traverse: map[string]bool{},
		}
		linkJSON := []naiveLinkJSON{}

		mLinks := r.Intn(n*n + 1)
		created := map[string]bool{}
		for k := 0; k < mLinks; k++ {
			src := ids[r.Intn(n)]
			dst := ids[r.Intn(n)]
			typ := "dir"
			bidir := false
			if r.Intn(2) == 0 {
				typ, bidir = "bi", true
			}
			lid := linkIDFor(k)
			if created[lid] {
				continue
			}
			l := Link{ID: lid, Type: typ, Source: src, Target: dst}
			if err := g.CreateLink(l); err != nil {
				continue // 非法自环/重复端点被模型拒绝是正常现象
			}
			created[lid] = true
			sc.links = append(sc.links, naiveLink{id: lid, from: src, to: dst, bidir: bidir})
			linkJSON = append(linkJSON, naiveLinkJSON{ID: lid, From: src, To: dst, Bidir: bidir})
		}

		var existObjs, traverseLinks []string
		for _, o := range ids {
			if r.Intn(100) < 70 {
				sc.exist[o] = true
				existObjs = append(existObjs, o)
			}
		}
		for _, l := range sc.links {
			if r.Intn(100) < 70 {
				sc.traverse[l.id] = true
				traverseLinks = append(traverseLinks, l.id)
			}
		}
		grant(g, "caller", existObjs, traverseLinks...)

		res, err := g.HasCycle("caller")
		if err != nil {
			t.Fatalf("iter %d: HasCycle error: %v", iter, err)
		}
		naiveHas, naiveBest := naiveHasCycle(sc)

		rec := callRecord{
			Iteration:  iter,
			Caller:     "caller",
			Objects:    sc.objects,
			Links:      linkJSON,
			Exist:      existObjs,
			Traverse:   traverseLinks,
			HasCycle:   res.HasCycle,
			Evidence:   res.Evidence,
			NaiveCycle: naiveHas,
			NaiveBest:  naiveBest,
		}
		if err := enc.Encode(rec); err != nil {
			t.Fatal(err)
		}

		if res.HasCycle != naiveHas {
			t.Fatalf("iter %d mismatch: production=%v naive=%v scenario=%+v", iter, res.HasCycle, naiveHas, sc)
		}
		if res.HasCycle {
			verifyEvidenceIsRealCycle(t, sc, res.Evidence)
			if !sameSet(res.Evidence, naiveBest) {
				t.Fatalf("iter %d evidence mismatch: production=%v naive=%v", iter, res.Evidence, naiveBest)
			}
		} else if res.Evidence != nil {
			t.Fatalf("iter %d: acyclic result must carry nil evidence, got %v", iter, res.Evidence)
		}
	}
}

func idFor(i int) string {
	// 使用等宽 ID，避免字典序与数字序不一致带来的可读性问题。
	return "o" + string(rune('a'+i%26)) + repeatByte('0', i/26)
}

func linkIDFor(k int) string {
	return "l" + string(rune('a'+k%26)) + repeatByte('0', k/26)
}

func repeatByte(b byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return string(out)
}
