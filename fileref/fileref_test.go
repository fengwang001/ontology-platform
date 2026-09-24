package fileref

import (
	"fmt"
	"testing"

	"ontology/snapchain"
)

// 可删除文件靠引用计数判定：Expire 的访问个数不随保留快照的文件总数 m 增长。
// 构造 S1{f0}、S2{m 个新文件}，Expire 只过期 S1，访问个数恒为 1 快照 + 1 引用。
func TestExpireVisitsIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := &Store{}
		s.Register([]string{"f0"}, []string{"f0"})
		add := make([]string, m)
		for i := range add {
			add[i] = fmt.Sprintf("m%d", i)
		}
		s.Register(add, add)
		del := s.ApplyExpire([]snapchain.Snapshot{{ID: 1, Ts: 1, Files: []string{"f0"}}})
		if fmt.Sprint(del) != "[f0]" {
			t.Fatalf("m=%d: del=%v, want [f0]", m, del)
		}
		if s.visited != 2 { // 过期快照个数(1) + 其文件引用数(1)，与 m 无关
			t.Fatalf("m=%d: visited=%d, want 2（不随 m 增长）", m, s.visited)
		}
		if len(s.Files()) != m {
			t.Fatalf("m=%d: files=%d, want %d", m, len(s.Files()), m)
		}
	}
}

// 引用计数基本语义：共享文件在一个快照过期后仍可读，归零才删除。
func TestRefcountBasic(t *testing.T) {
	s := &Store{}
	s.Register([]string{"a", "b"}, []string{"a", "b"})
	s.Register([]string{"c"}, []string{"b", "c"})
	if !s.Seen("a") || !s.Seen("c") || s.Seen("zz") {
		t.Fatal("Seen 登记错误")
	}
	if del := s.ApplyExpire([]snapchain.Snapshot{{ID: 1, Ts: 1, Files: []string{"a", "b"}}}); fmt.Sprint(del) != "[a]" {
		t.Fatalf("del=%v, want [a]", del)
	}
	if fmt.Sprint(s.Files()) != "[b c]" {
		t.Fatalf("files=%v, want [b c]", s.Files())
	}
	if del := s.ApplyExpire(nil); len(del) != 0 || s.visited != 0 {
		t.Fatalf("空过期应幂等: del=%v visited=%d", del, s.visited)
	}
}
