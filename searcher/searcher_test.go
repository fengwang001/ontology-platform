package searcher

import "testing"

func docsToSeqs(docs []Doc) map[string]int64 {
	m := map[string]int64{}
	for _, d := range docs {
		m[d.ID] = d.Seq
	}
	return m
}

func TestApplySearchCommitLoad(t *testing.T) {
	s := New()
	s.Apply(1, false, "a", []byte("v1"))
	s.Apply(2, false, "b", []byte("v2"))
	docs := s.Search()
	if len(docs) != 2 || docs[0].ID != "a" || docs[1].ID != "b" {
		t.Fatalf("Search 未按字节序返回 a,b: %+v", docs)
	}
	if string(docs[0].Body) != "v1" || docs[0].Seq != 1 {
		t.Fatalf("文档内容错误: %+v", docs[0])
	}

	// 删除墓碑进入内存段：Search 不可见，但 Lookup 能查到墓碑
	s.Apply(3, true, "a", nil)
	if docs := s.Search(); len(docs) != 1 || docs[0].ID != "b" {
		t.Fatalf("删除 a 后 Search=%+v, 期望只剩 b", docs)
	}
	if d, ok := s.Lookup("a"); !ok || !d.Deleted || d.Seq != 3 {
		t.Fatalf("Lookup(a)=%+v ok=%v, 期望墓碑 seq3", d, ok)
	}

	// Commit：内存段并入提交点后清空
	s.Commit()
	s.Apply(4, false, "c", []byte("v4"))
	docs = s.Search()
	if got := docsToSeqs(docs); got["b"] != 2 || got["c"] != 4 {
		t.Fatalf("Commit 后 Search=%v, 期望 b@2,c@4", got)
	}

	// Snapshot 只含存活文档
	snap := s.Snapshot()
	if _, hasA := snap["a"]; hasA {
		t.Fatalf("Snapshot 不应包含已删除的 a")
	}
	snap["b"] = Doc{ID: "b", Seq: 999} // 改快照不影响视图
	if d, _ := s.Lookup("b"); d.Seq != 2 {
		t.Fatalf("Snapshot 不是深拷贝, 视图被外部修改 seq=%d", d.Seq)
	}

	// Load 装回提交点并清空内存段：c 来自内存段，应消失
	s.Load(snap)
	if docs := s.Search(); len(docs) != 1 || docs[0].ID != "b" || docs[0].Seq != 999 {
		t.Fatalf("Load 后 Search=%+v, 期望仅 b@999", docs)
	}
}

func TestSearchReturnsBodyCopies(t *testing.T) {
	s := New()
	s.Apply(1, false, "a", []byte("orig"))
	docs := s.Search()
	docs[0].Body[0] = 'X'
	again := s.Search()
	if string(again[0].Body) != "orig" {
		t.Fatalf("Search 未返回 body 副本: %q", again[0].Body)
	}
}
