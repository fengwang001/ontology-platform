package store

import (
	"fmt"
	"sync"
	"testing"
)

func TestVersionedRead(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		old, new  []byte
		wantOld   []byte
		wantOKNew bool
	}{
		{"普通键被改写后旧版本仍读旧值", "k", []byte("v1"), []byte("v2"), []byte("v1"), true},
		{"空键合法", "", []byte("v1"), []byte("v2"), []byte("v1"), true},
		{"空值与不存在可区分", "e", []byte{}, []byte("v2"), []byte{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			s.Put(c.key, c.old)
			v := s.RegisterSnapshot()
			s.Put(c.key, c.new)
			got, ok := s.GetAt(c.key, v)
			if !ok || string(got) != string(c.wantOld) {
				t.Fatalf("旧水位读 = %q,%v；期望 %q,true", got, ok, c.wantOld)
			}
			if got, _ := s.GetAt(c.key, s.Version()); string(got) != string(c.new) {
				t.Fatalf("新水位读 = %q；期望 %q", got, c.new)
			}
			if _, ok := s.GetAt("不存在", s.Version()); ok {
				t.Fatal("不存在的键应 ok=false")
			}
			s.ReleaseSnapshot(v)
		})
	}
}

func TestRetainedAndRelease(t *testing.T) {
	cases := []struct {
		name       string
		keys       int
		overwrites int
		snapshots  int
		wantOpen   int // 快照开着时的保留值个数
	}{
		{"无快照改写不产生保留值", 100, 100, 0, 0},
		{"单快照改写100键保留100", 100000, 100, 1, 100},
		{"单快照改写全部键保留全部", 1000, 1000, 1, 1000},
		{"两个重叠快照改写100键保留100", 1000, 100, 2, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			for i := 0; i < c.keys; i++ {
				s.Put(fmt.Sprintf("k%06d", i), []byte("old"))
			}
			versions := make([]uint64, c.snapshots)
			for i := range versions {
				versions[i] = s.RegisterSnapshot()
			}
			for i := 0; i < c.overwrites; i++ {
				s.Put(fmt.Sprintf("k%06d", i), []byte("new"))
			}
			if got := s.Retained(); got != c.wantOpen {
				t.Fatalf("快照开着时保留值 = %d；期望 %d", got, c.wantOpen)
			}
			// 逐个关闭：只要还有一个快照开着，保留值不得释放。
			for i, v := range versions {
				s.ReleaseSnapshot(v)
				want := 0
				if i < len(versions)-1 {
					want = c.wantOpen
				}
				if got := s.Retained(); got != want {
					t.Fatalf("关闭第 %d 个快照后保留值 = %d；期望 %d", i+1, got, want)
				}
			}
		})
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	s := New()
	v := s.RegisterSnapshot()
	defer s.ReleaseSnapshot(v)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				s.Put(fmt.Sprintf("k%d", i%64), []byte{byte(w)})
				s.GetAt(fmt.Sprintf("k%d", i%64), v)
			}
		}(w)
	}
	wg.Wait()
	if s.Reads() != 8*500 {
		t.Fatalf("读取计数 = %d；期望 %d", s.Reads(), 8*500)
	}
}
