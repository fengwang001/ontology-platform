package snapshot

import "testing"

// 1. 正常导出：校验通过、聚合可生成。
func TestHappyPath(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{
			{ID: "p1", Data: []byte("a"), Refs: []CrossTypeRef{{Field: "worksFor", TargetType: "Company", TargetID: "c1"}}},
		}},
		"Company": {{
			{ID: "c1", Data: []byte("b")},
		}},
	}})

	l := &sliceLogger{}
	res, err := NewLoader(dir).Load([]string{"Person", "Company"}, l)
	l.print(t)
	if err != nil || len(res.Issues) != 0 {
		t.Fatalf("expected no issues, got %+v err=%v", res.Issues, err)
	}
	for _, rep := range res.Reports {
		if rep.Status != StatusTrusted {
			t.Fatalf("chunk %v status=%s", rep.Chunk, rep.Status)
		}
	}

	aggLog := &sliceLogger{}
	a, err := NewLoader(dir).Aggregate([]string{"Person", "Company"}, aggLog)
	aggLog.print(t)
	if err != nil || !a.Aggregatable {
		t.Fatalf("expected aggregatable, got %+v err=%v", a, err)
	}
	if len(a.Objects["Person"]) != 1 || len(a.Objects["Company"]) != 1 {
		t.Fatalf("aggregate objects mismatch: %+v", a.Objects)
	}
}

// 2. 范围外请求：最高优先级；范围内块仍独立校验可取。
func TestOutOfRange(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{{ID: "p1"}}},
	}})

	l := &sliceLogger{}
	res, _ := NewLoader(dir).Load([]string{"Person", "Ghost"}, l)
	l.print(t)
	var found bool
	for _, is := range res.Issues {
		if is.Kind == KindOutOfRange && is.Chunk.Type == "Ghost" {
			found = true
		}
	}
	if !found {
		t.Fatalf("out-of-range issue missing: %+v", res.Issues)
	}
	if len(res.Issues) == 0 || res.Issues[0].Kind != KindOutOfRange {
		t.Fatalf("out-of-range must have highest priority, got %+v", res.Issues)
	}
	if res.Reports[ChunkRef{Type: "Person", Chunk: 0}].Status != StatusTrusted {
		t.Fatalf("in-scope chunk must still be verified")
	}

	agg, _ := NewLoader(dir).Aggregate([]string{"Ghost"}, &sliceLogger{})
	if agg.Aggregatable {
		t.Fatalf("aggregate of out-of-range type must fail")
	}
}

// 3. 完整性失败：结论只针对本块，其他块不受影响。
func TestIntegrityFailure(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person":  {{{ID: "p1", Data: []byte("orig")}}},
		"Company": {{{ID: "c1"}}},
	}})

	env := loadChunkFile(t, dir, "Person", 0)
	env.Records[0].Data = []byte("tampered")
	writeChunkFile(t, dir, "Person", 0, env)

	l := &sliceLogger{}
	res, _ := NewLoader(dir).Load([]string{"Person", "Company"}, l)
	l.print(t)
	person := res.Reports[ChunkRef{Type: "Person", Chunk: 0}]
	if person.Status != StatusIntegrityBad || person.Records != nil {
		t.Fatalf("expected integrity failure with no trusted records, got %+v", person)
	}
	company := res.Reports[ChunkRef{Type: "Company", Chunk: 0}]
	if company.Status != StatusTrusted || len(company.Records) != 1 {
		t.Fatalf("unrelated chunk must remain trusted, got %+v", company)
	}

	agg, _ := NewLoader(dir).Aggregate([]string{"Person", "Company"}, &sliceLogger{})
	if agg.Aggregatable {
		t.Fatalf("aggregate must not be generatable")
	}
	// 聚合不可生成不影响单块取出。
	if got := res.Reports[ChunkRef{Type: "Company", Chunk: 0}]; len(got.Records) != 1 {
		t.Fatalf("single trusted chunk must still be retrievable")
	}
}

// 4. 数量不一致：整块不可信，即使其中记录各自完好。
func TestCountMismatchWholeChunkUntrusted(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{
			{ID: "p1", Data: []byte("x")},
			{ID: "p2", Data: []byte("y")},
		}},
	}})

	env := loadChunkFile(t, dir, "Person", 0)
	env.Header.DeclaredCount = 3 // 仅改声明条数，checksum 仍匹配
	writeChunkFile(t, dir, "Person", 0, env)

	l := &sliceLogger{}
	res, _ := NewLoader(dir).Load([]string{"Person"}, l)
	l.print(t)
	rep := res.Reports[ChunkRef{Type: "Person", Chunk: 0}]
	if rep.Status != StatusCountMismatch {
		t.Fatalf("expected count mismatch, got %s", rep.Status)
	}
	if rep.Records != nil {
		t.Fatalf("all records in a count-mismatched chunk must be untrusted")
	}
	var hasCount bool
	for _, is := range res.Issues {
		if is.Kind == KindCountMismatch && is.Chunk.Type == "Person" {
			hasCount = true
		}
	}
	if !hasCount {
		t.Fatalf("count mismatch issue missing")
	}

	// 截断场景：内容重新校验通过但实际条数少于声明。
	dir2 := t.TempDir()
	mustExport(t, dir2, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{{ID: "p1"}, {ID: "p2"}}},
	}})
	env2 := loadChunkFile(t, dir2, "Person", 0)
	env2.Records = env2.Records[:1]
	sum, _ := computeChecksum(env2.Records)
	env2.Checksum = sum
	writeChunkFile(t, dir2, "Person", 0, env2)
	res2, _ := NewLoader(dir2).Load([]string{"Person"}, nil)
	rep2 := res2.Reports[ChunkRef{Type: "Person", Chunk: 0}]
	if rep2.Status != StatusCountMismatch || rep2.Records != nil {
		t.Fatalf("truncated chunk must be count-mismatched and untrusted, got %+v", rep2)
	}
}
