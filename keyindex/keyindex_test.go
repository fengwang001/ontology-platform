package keyindex

import (
	"reflect"
	"testing"
)

func TestSeekGE(t *testing.T) {
	ix := New()
	for _, k := range []string{"a", "b", "b/2", "c"} {
		ix.Put(k, 1, false)
	}
	cases := []struct {
		k      string
		key    string
		ok     bool
		afters []string
	}{
		{"", "a", true, []string{"b", "b/2", "c"}},
		{"a", "a", true, nil},
		{"a0", "b", true, nil},
		{"b/1", "b/2", true, nil},
		{"b/2", "b/2", true, nil},
		{"b/3", "c", true, nil},
		{"d", "", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.k, func(t *testing.T) {
			s := ix.Snapshot()
			got, ok := s.SeekGE(tc.k)
			if ok != tc.ok || (ok && got != tc.key) {
				t.Fatalf("SeekGE(%q)=(%q,%v) 期望 (%q,%v)", tc.k, got, ok, tc.key, tc.ok)
			}
			for i, want := range tc.afters {
				s.Next()
				g, ok := s.Current()
				if !ok || g != want {
					t.Fatalf("Next#%d = %q,%v 期望 %q", i, g, ok, want)
				}
			}
			t.Logf("SeekGE(%q) => %q(ok=%v)；后继 Next 不增加寻址计数", tc.k, got, ok)
		})
	}
}

func TestSeekCounter(t *testing.T) {
	ix := New()
	ix.Put("a", 1, false)
	ix.ResetSeeks()
	s := ix.Snapshot()
	s.SeekGE("a")
	s.SeekGE("z")
	s.Next()
	if got := ix.Seeks(); got != 2 {
		t.Fatalf("两次 SeekGE 后 seeks=%d 期望 2（Next 不计数）", got)
	}
	t.Logf("seeks=%d 判定=仅 SeekGE 计数，Next/Current 不计数", ix.Seeks())
}

func TestVersionsDesc(t *testing.T) {
	ix := New()
	ix.Put("x", 3, false)
	ix.Put("x", 1, true)
	ix.Put("x", 2, false)
	got := ix.Snapshot().Versions("x")
	want := []Version{{3, false}, {2, false}, {1, true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Versions=%v 期望 %v", got, want)
	}
	t.Logf("Versions(x)=%v 判定=版本号降序含标记", got)
}

func TestSucc(t *testing.T) {
	cases := []struct {
		in   string
		out  string
		ok   bool
		note string
	}{
		{"b/", "b0", true, "末字节非0xFF直接进位"},
		{"a\xFF", "b", true, "0xFF 被剥后进位"},
		{"a\xFF\xFF", "b", true, "连续0xFF全部剥除"},
		{"", "", false, "空前缀无后继（=整个键空间）"},
		{"\xFF", "", false, "全0xFF无后继"},
		{"\xFF\xFF", "", false, "全0xFF无后继"},
		{"abc", "abd", true, "普通进位"},
		{"ab\xFE", "ab\xFF", true, "FE->FF"},
	}
	for _, tc := range cases {
		t.Run(tc.note, func(t *testing.T) {
			got, ok := Succ(tc.in)
			if ok != tc.ok || got != tc.out {
				t.Fatalf("Succ(%q)=(%q,%v) 期望 (%q,%v)；%s", tc.in, got, ok, tc.out, tc.ok, tc.note)
			}
			t.Logf("Succ(%q)=(%q,%v) 判定=%s", tc.in, got, ok, tc.note)
		})
	}
}

func TestDeleteVersion(t *testing.T) {
	ix := New()
	ix.Put("a", 1, false)
	ix.Put("a", 2, true)
	ix.DeleteVersion("a", 2)
	if got := ix.Snapshot().Versions("a"); len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("删除版本后=%v", got)
	}
	ix.DeleteVersion("a", 1)
	if _, ok := ix.Snapshot().SeekGE("a"); ok {
		t.Fatal("最后一个版本删除后键应消失")
	}
	t.Log("删除全部版本后键从索引移除")
}

func TestSnapshotIsolation(t *testing.T) {
	ix := New()
	ix.Put("a", 1, false)
	s := ix.Snapshot()
	ix.Put("b", 2, false)
	if _, ok := s.SeekGE("b"); ok {
		t.Fatal("旧快照不应看到新写入")
	}
	t.Log("快照建立后的写入对该快照不可见（copy-on-write）")
}
