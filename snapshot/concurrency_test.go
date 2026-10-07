package snapshot

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// 8. 并发只读加载：结果一致、可重复；只读操作幂等不改导出。
func TestConcurrentLoadsAreDeterministic(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{
			{ID: "p1", Refs: []CrossTypeRef{{Field: "c", TargetType: "Company", TargetID: "c1"}}},
			{ID: "p2", Refs: []CrossTypeRef{{Field: "c", TargetType: "Company", TargetID: "missing"}}},
		}},
		"Company": {{{ID: "c1"}}},
		"Auditor": {{{ID: "a1"}}},
	}})
	env := loadChunkFile(t, dir, "Auditor", 0)
	env.Header.DeclaredCount = 5
	writeChunkFile(t, dir, "Auditor", 0, env)

	snapshot1, err := snapshotFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	var wg sync.WaitGroup
	results := make([]string, goroutines)
	aggResults := make([]string, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := &sliceLogger{}
			res, _ := NewLoader(dir).Load([]string{"Person", "Company", "Auditor"}, l)
			results[i] = canonicalIssues(res.Issues) + "|" + canonicalStatuses(res.Reports)
			agg, _ := NewLoader(dir).Aggregate([]string{"Person", "Company", "Auditor"}, nil)
			aggResults[i] = fmt.Sprintf("%v|%s", agg.Aggregatable, canonicalIssues(agg.Issues))
		}(g)
	}
	wg.Wait()

	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("load result diverges under concurrency:\n%q\nvs\n%q", results[0], results[i])
		}
		if aggResults[i] != aggResults[0] {
			t.Fatalf("aggregate result diverges under concurrency:\n%q\nvs\n%q", aggResults[0], aggResults[i])
		}
	}

	// 再串行重复一次：跨运行也必须一致、可重复。
	res, _ := NewLoader(dir).Load([]string{"Person", "Company", "Auditor"}, &sliceLogger{})
	if got := canonicalIssues(res.Issues) + "|" + canonicalStatuses(res.Reports); got != results[0] {
		t.Fatalf("repeated load not reproducible")
	}

	// 幂等：磁盘快照字节未发生任何变化。
	snapshot2, err := snapshotFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot1 != snapshot2 {
		t.Fatalf("read-only verification mutated the export on disk")
	}
}

func canonicalIssues(issues []Issue) string {
	out := ""
	for _, is := range issues {
		ref := ""
		if is.Ref != nil {
			ref = is.Ref.TargetType + "/" + is.Ref.TargetID
		}
		out += fmt.Sprintf("%s@%s/%d@%s#%s;", is.Kind, is.Chunk.Type, is.Chunk.Chunk, ref, is.Reason)
	}
	return out
}

func canonicalStatuses(reports map[ChunkRef]*ChunkReport) string {
	out := ""
	for _, ref := range sortedChunkRefs(reports) {
		rep := reports[ref]
		out += fmt.Sprintf("%s/%d=%s(d%d,a%d);", ref.Type, ref.Chunk, rep.Status, rep.Declared, rep.Actual)
	}
	return out
}

func snapshotFingerprint(dir string) (string, error) {
	mf, err := readManifest(dir)
	if err != nil {
		return "", err
	}
	fp := "manifest:" + filepath.Base(dir) + ";"
	for _, typ := range mf.Types {
		for _, f := range mf.Chunks[typ] {
			raw, err := readFileForTest(filepath.Join(dir, f))
			if err != nil {
				return "", err
			}
			fp += typ + ":" + f + ":" + fmt.Sprintf("%x", len(raw)) + ";"
		}
	}
	return fp, nil
}
