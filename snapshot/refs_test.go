package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

// 5. 悬空引用 与 目标块不可信时的保守结论，二者不得混报。
func TestDanglingAndConservative(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{
			{ID: "p1", Refs: []CrossTypeRef{
				{Field: "worksFor", TargetType: "Company", TargetID: "c_missing"},
				{Field: "auditedBy", TargetType: "Auditor", TargetID: "a1"},
				{Field: "ext", TargetType: "Unknown", TargetID: "u1"},
			}},
		}},
		"Company": {{{ID: "c1"}}},
		"Auditor": {{{ID: "a1"}}},
	}})

	env := loadChunkFile(t, dir, "Auditor", 0)
	env.Header.DeclaredCount = 99
	writeChunkFile(t, dir, "Auditor", 0, env)

	l := &sliceLogger{}
	res, _ := NewLoader(dir).Load([]string{"Person", "Company", "Auditor"}, l)
	l.print(t)

	var dangling, conservative, uncovered, countBad int
	for _, is := range res.Issues {
		switch is.Kind {
		case KindDanglingRef:
			if is.Ref.TargetType == "Company" && is.Ref.TargetID == "c_missing" {
				dangling++
			}
		case KindRefUnverifiable:
			if is.Ref != nil && is.Ref.TargetType == "Auditor" {
				conservative++
			}
			if is.Ref != nil && is.Ref.TargetType == "Unknown" {
				uncovered++
			}
		case KindCountMismatch:
			if is.Chunk.Type == "Auditor" {
				countBad++
			}
		}
	}
	if dangling != 1 {
		t.Fatalf("expected exactly 1 dangling ref, got %d", dangling)
	}
	if conservative != 1 {
		t.Fatalf("expected conservative unverifiable for untrusted target, got %d", conservative)
	}
	if uncovered != 1 {
		t.Fatalf("expected unverifiable for uncovered target type, got %d", uncovered)
	}
	if countBad != 1 {
		t.Fatalf("auditor count mismatch missing")
	}
	for _, is := range res.Issues {
		if is.Kind == KindDanglingRef && is.Ref != nil &&
			(is.Ref.TargetType == "Auditor" || is.Ref.TargetType == "Unknown") {
			t.Fatalf("must not label an unverifiable reference as dangling")
		}
	}

	agg, _ := NewLoader(dir).Aggregate([]string{"Person", "Company"}, nil)
	if agg.Aggregatable {
		t.Fatalf("aggregate must fail on dangling/unverifiable references")
	}
	// 只取干净类型仍可聚合。
	cleanAgg, _ := NewLoader(dir).Aggregate([]string{"Company"}, nil)
	if !cleanAgg.Aggregatable {
		t.Fatalf("clean-only aggregate must be generatable")
	}
}

// 6. 双向跨块引用：两个方向独立判定。
func TestBidirectionalIndependent(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{
			{ID: "p1", Refs: []CrossTypeRef{{Field: "employer", TargetType: "Company", TargetID: "c1"}}},
		}},
		"Company": {{
			{ID: "c1", Refs: []CrossTypeRef{{Field: "ceo", TargetType: "Person", TargetID: "p9"}}},
		}},
	}})

	l := &sliceLogger{}
	res, _ := NewLoader(dir).Load([]string{"Person", "Company"}, l)
	l.print(t)
	var dangling int
	for _, is := range res.Issues {
		if is.Kind == KindDanglingRef && is.Chunk.Type == "Person" {
			t.Fatalf("p1 -> c1 must be resolved, not dangling")
		}
		if is.Kind == KindDanglingRef && is.Chunk.Type == "Company" &&
			is.Ref.TargetType == "Person" && is.Ref.TargetID == "p9" {
			dangling++
		}
	}
	if dangling != 1 {
		t.Fatalf("reverse direction must be independently dangling, got %d", dangling)
	}

	// 双向都有效时聚合可生成，且两个方向各自记录一条 resolved 判定。
	dir2 := t.TempDir()
	mustExport(t, dir2, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{
			{ID: "p1", Refs: []CrossTypeRef{{Field: "employer", TargetType: "Company", TargetID: "c1"}}},
		}},
		"Company": {{
			{ID: "c1", Refs: []CrossTypeRef{{Field: "ceo", TargetType: "Person", TargetID: "p1"}}},
		}},
	}})
	log2 := &sliceLogger{}
	agg, _ := NewLoader(dir2).Aggregate([]string{"Person", "Company"}, log2)
	log2.print(t)
	if !agg.Aggregatable {
		t.Fatalf("mutual valid references must aggregate")
	}
	var resolved int
	for _, d := range log2.decisions {
		if d.Stage == "reference" && d.Output == "resolved" {
			resolved++
		}
	}
	if resolved != 2 {
		t.Fatalf("each direction must be judged independently (2 resolved), got %d", resolved)
	}
}

// 7. 拒绝优先级：out_of_range > integrity > count > dangling/unverifiable。
func TestRejectionPriority(t *testing.T) {
	dir := t.TempDir()
	mustExport(t, dir, ExportRequest{Chunks: map[string][][]Record{
		"Person": {{{ID: "p1"}}},
		"Source": {{
			{ID: "s1", Refs: []CrossTypeRef{{Field: "a", TargetType: "Company", TargetID: "nope"},
				{Field: "b", TargetType: "Auditor", TargetID: "a1"}}},
		}},
		"Company": {{{ID: "c1"}}},
		"Auditor": {{{ID: "a1"}}},
	}})
	// Person 块直接损坏（完整性失败）；Auditor 数量不一致；
	// Source 干净：一条悬空（Company 可信但无 nope），一条无法校验（Auditor 不可信）。
	env := loadChunkFile(t, dir, "Auditor", 0)
	env.Header.DeclaredCount = 7
	writeChunkFile(t, dir, "Auditor", 0, env)
	if err := os.WriteFile(filepath.Join(dir, chunkFileName("Person", 0)), []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := &sliceLogger{}
	res, _ := NewLoader(dir).Load([]string{"Person", "Source", "Company", "Auditor", "Ghost"}, l)
	l.print(t)
	if len(res.Issues) < 5 {
		t.Fatalf("expected all issue classes reported, got %+v", res.Issues)
	}
	wantOrder := []IssueKind{
		KindOutOfRange,
		KindIntegrityFailure,
		KindCountMismatch,
	}
	for i, want := range wantOrder {
		if res.Issues[i].Kind != want {
			t.Fatalf("priority position %d: want %s got %s (all=%v)",
				i, want, res.Issues[i].Kind, issueKinds(res.Issues))
		}
	}
	// 悬空引用仍须保留报告（Person 已损坏所以不再产生引用问题；
	// 这里通过 Company 的干净块确认末位类别存在）。
	lastKinds := map[IssueKind]bool{}
	for _, is := range res.Issues[3:] {
		lastKinds[is.Kind] = true
	}
	if !lastKinds[KindDanglingRef] || !lastKinds[KindRefUnverifiable] {
		t.Fatalf("expected dangling and unverifiable ref issues, issues=%+v", res.Issues)
	}
}
