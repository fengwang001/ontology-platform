package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"ontology/phrase"
	"ontology/posting"
	"ontology/segment"
)

func buildSeg(m map[string][][2]uint32) *segment.Segment {
	lists := map[string]posting.List{}
	for term, pts := range m {
		var b posting.Builder
		for _, p := range pts {
			_ = b.Add(p[0], p[1])
		}
		lists[term] = b.List()
	}
	return segment.New(lists)
}

func TestMergeCounts(t *testing.T) {
	cases := []struct {
		name    string
		segs    []*segment.Segment
		deleted map[uint32]bool
		reads   int
		writes  int
		docsA   []uint32 // 词 "a" 合并后的文档号
	}{
		{"union no deletion", []*segment.Segment{
			buildSeg(map[string][][2]uint32{"a": {{0, 0}, {2, 1}}, "b": {{1, 0}}}),
			buildSeg(map[string][][2]uint32{"a": {{5, 0}, {7, 0}}, "c": {{9, 0}}}),
		}, nil, 6, 6, []uint32{0, 2, 5, 7}},
		{"with deletion", []*segment.Segment{
			buildSeg(map[string][][2]uint32{"a": {{0, 0}, {2, 1}}, "b": {{1, 0}}}),
			buildSeg(map[string][][2]uint32{"a": {{5, 0}, {7, 0}}, "c": {{9, 0}}}),
		}, map[uint32]bool{2: true, 9: true}, 6, 4, []uint32{0, 5, 7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var st Stats
			m := Merge(tc.segs, tc.deleted, &st)
			if st.Reads != tc.reads || st.Writes != tc.writes {
				t.Fatalf("reads/writes: got %d/%d, want %d/%d",
					st.Reads, st.Writes, tc.reads, tc.writes)
			}
			var docs []uint32
			for _, p := range m.Lists["a"] {
				docs = append(docs, p.Doc)
			}
			if !reflect.DeepEqual(docs, tc.docsA) {
				t.Fatalf("docs of a: got %v, want %v", docs, tc.docsA)
			}
		})
	}
}

func TestDeleteThenQueryEqualsMergeThenQuery(t *testing.T) {
	c := New("")
	c.AddDoc([]string{"a", "b", "a"})
	c.AddDoc([]string{"b", "c"})
	c.AddDoc([]string{"a", "c"})
	c.Delete(2)
	before := c.Phrase("a")
	if err := c.Compact(); err != nil {
		t.Fatal(err)
	}
	after := c.Phrase("a")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("before %v != after %v", before, after)
	}
	want := []phrase.Hit{{Doc: 0, Start: 0}, {Doc: 0, Start: 2}}
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("got %v, want %v", after, want)
	}
	if c.Segments() != 1 {
		t.Fatalf("segments: got %d, want 1", c.Segments())
	}
}

func TestCrashMidMergeRecovery(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	c.AddDoc([]string{"a", "b"})
	c.AddDoc([]string{"a", "c"})
	if err := c.Compact(); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%v|%v", c.Phrase("a"), c.And("a", "b"))
	// 模拟合并中途崩溃：留下半截 tmp 文件
	half := filepath.Join(dir, "merged.seg.tmp")
	if err := os.WriteFile(half, []byte("OSG1\x01\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := Recover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(half); !os.IsNotExist(err) {
		t.Fatal("half-written tmp segment not cleaned")
	}
	got := fmt.Sprintf("%v|%v", rc.Phrase("a"), rc.And("a", "b"))
	if got != want {
		t.Fatalf("query results differ after crash:\nbefore %s\nafter  %s", want, got)
	}
}

func TestConcurrentQueryAndMerge(t *testing.T) {
	c := New("")
	for i := 0; i < 20; i++ {
		c.AddDoc([]string{"a", "b", "c"})
	}
	wantPhrase := c.Phrase("a", "b")
	wantAnd := c.And("a", "c")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := c.Phrase("a", "b"); !reflect.DeepEqual(got, wantPhrase) {
					t.Errorf("phrase mismatch: %v", got)
					return
				}
				if got := c.And("a", "c"); !reflect.DeepEqual(got, wantAnd) {
					t.Errorf("and mismatch: %v", got)
					return
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		if err := c.Compact(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
