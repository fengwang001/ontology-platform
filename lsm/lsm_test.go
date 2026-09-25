package lsm

import (
	"fmt"
	"testing"

	"ontology/mem"
)

// 构造含 m 个 key 的单个 SSTable，Get 其中一个 key：
// 单个 SSTable 内定位 key 的检查个数不得随 m 线性增长（map 定位，常数）。
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := &Store{mt: mem.New(1)}
		tab := sstable{m: make(map[string]mem.Entry, m)}
		for i := 0; i < m; i++ {
			tab.m[fmt.Sprintf("k%06d", i)] = mem.Entry{Val: int64(i), Seq: uint64(i + 1)}
		}
		s.ssts = []sstable{tab}
		v, ok, del := s.Get(fmt.Sprintf("k%06d", m-1))
		if !ok || del || v != int64(m-1) {
			t.Fatalf("m=%d: Get got (%d,%v,%v)", m, v, ok, del)
		}
		if s.probe > 1 { // 与 m 无关的小常数：map 定位只查 1 个条目
			t.Fatalf("m=%d: probe=%d grows with table size", m, s.probe)
		}
		if s.readAmp != 1 {
			t.Fatalf("m=%d: readAmp=%d, want 1", m, s.readAmp)
		}
	}
}

// Compact 合并规则：seq 最大者胜；胜者墓碑仅当存在更旧值时保留。
func TestCompactTombstoneRule(t *testing.T) {
	// maxMem=1：每条写都把上一条冻结成单条目 SSTable，seq 即写入顺序。
	build := func(ops ...string) *Store {
		s := NewStore(1)
		for i, op := range ops {
			if op[0] == 'P' {
				s.Put("k", int64(i+1))
			} else {
				s.Del("k")
			}
		}
		s.Put("other", 1) // 触发最后一次冻结
		return s
	}
	cases := []struct {
		name      string
		ops       []string
		wantTomb  bool // 合并表中 k 是否保留墓碑
		wantVal   int64
		wantExist bool // 合并表中 k 是否存在
	}{
		{"tomb over older value kept", []string{"P", "D"}, true, 0, true},
		{"lone tomb dropped", []string{"D"}, false, 0, false},
		{"newest value wins", []string{"P", "P", "P"}, false, 3, true},
		{"value after tomb wins", []string{"D", "P"}, false, 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := build(c.ops...)
			nBefore := s.NumSST()
			s.Compact()
			if s.NumSST() != 1 {
				t.Fatalf("NumSST=%d, want 1 (was %d)", s.NumSST(), nBefore)
			}
			e, exist := s.ssts[0].m["k"]
			if exist != c.wantExist {
				t.Fatalf("k exist=%v, want %v", exist, c.wantExist)
			}
			if exist && (e.Tomb != c.wantTomb || (!e.Tomb && e.Val != c.wantVal)) {
				t.Fatalf("k=(%d,tomb=%v), want val=%d tomb=%v", e.Val, e.Tomb, c.wantVal, c.wantTomb)
			}
		})
	}
}

// 不变量2：seq 最大的写决定结果（第三节八步场景）。
func TestNewestWins(t *testing.T) {
	s := NewStore(2)
	s.Put("k1", 1)
	s.Put("k2", 2)
	s.Put("k1", 3)
	s.Del("k2")
	s.Put("k3", 4)
	if v, ok, _ := s.Get("k1"); !ok || v != 3 { // SST2 的 C 压过 SST1 的 A
		t.Fatalf("step6 Get(k1)=%d,%v, want 3,true", v, ok)
	}
	s.Del("k1")
	if _, ok, del := s.Get("k1"); ok || !del { // memtable 墓碑优先于 SSTable
		t.Fatalf("step8 Get(k1) ok=%v del=%v, want deleted", ok, del)
	}
}

// 不变量3：Del 后是「已删除」；Compact 保留压住旧值的墓碑；孤独墓碑被丢弃。
func TestTombstoneSemantics(t *testing.T) {
	s := NewStore(1) // maxMem=1：每条写冻结上一条，旧值必进 SSTable
	s.Put("x", 9)
	s.Del("x")
	if _, ok, del := s.Get("x"); ok || !del {
		t.Fatalf("after Del: ok=%v del=%v, want deleted", ok, del)
	}
	if _, ok, del := s.Get("ghost"); ok || del {
		t.Fatalf("never-written: ok=%v del=%v, want not-exist", ok, del)
	}
	s.Put("y", 1) // 把 x 的墓碑冻结进 SSTable
	s.Compact()
	if _, ok, del := s.Get("x"); ok || !del {
		t.Fatalf("after Compact: ok=%v del=%v, tomb must keep old value down", ok, del)
	}
	l := NewStore(2) // 孤独墓碑：值未进任何 SSTable，墓碑是合并范围内唯一痕迹
	l.Put("z", 1)
	l.Del("z")
	l.Put("w", 2)
	l.Put("q", 3) // 触发冻结
	l.Compact()
	if _, ok, del := l.Get("z"); ok || del {
		t.Fatalf("lone tomb: ok=%v del=%v, want not-exist", ok, del)
	}
}

// 墓碑保留的端到端效果：Compact 后旧值不复活。
func TestCompactNoResurrect(t *testing.T) {
	s := NewStore(1)
	s.Put("k", 7)
	s.Del("k")
	s.Put("x", 1) // 触发冻结
	s.Compact()
	if v, ok, del := s.Get("k"); ok || !del || v != 0 {
		t.Fatalf("Get(k)=(%d,%v,%v), want deleted", v, ok, del)
	}
}
