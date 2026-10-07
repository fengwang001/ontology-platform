package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSamePropertySerialEquivalence 并发写同一实例同一属性：
// 最终属性值与索引条目必须对应同一个串行顺序中的最后一次写入。
func TestConcurrentSamePropertySerialEquivalence(t *testing.T) {
	s, _, exact, prefix := newTestStore(NewBufferLogger())
	s.AddInstance("A")
	const writers = 32
	const rounds = 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				v := Of(fmt.Sprintf("w%02d-r%03d", w, r))
				if err := s.Write("A", "status", v); err != nil {
					t.Errorf("write failed: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	// 最终值必须是某次写入的值。
	v, err := s.Get("A", "status")
	if err != nil || !v.Present {
		t.Fatalf("final value=%+v err=%v", v, err)
	}
	// 值与所有索引结构对应同一次写入：按最终值查询必须命中且只命中 A。
	assertQuery(t, s, "status", v, "A")
	got, err := s.QueryByIndex("status", prefix.ID, PrefixKey(1)(v))
	if err != nil || len(got) != 1 || got[0] != "A" {
		t.Fatalf("prefix index mismatch for final value %+v: %v", v, got)
	}
	// 每个索引结构恰好一条条目（无错配残留）。
	if exact.Size() != 1 || prefix.Size() != 1 {
		t.Fatalf("index sizes %d/%d, want 1 each", exact.Size(), prefix.Size())
	}
	// 时钟推进数等于提交数（无丢失/重复提交）。
	if s.Clock() != writers*rounds {
		t.Fatalf("clock=%d, want %d", s.Clock(), writers*rounds)
	}
}

// TestConcurrentDifferentPropertiesUnblocked 并发写同一实例的不同属性：
// 互不阻塞地完成，最终结果等价于某个串行顺序。
func TestConcurrentDifferentPropertiesUnblocked(t *testing.T) {
	log := NewBufferLogger()
	s := NewStore(NewMemoryWAL(), NewFaultInjector(), log)
	const props = 8
	indexes := make([]*Index, props)
	for p := 0; p < props; p++ {
		name := fmt.Sprintf("prop-%d", p)
		idx := NewIndex(name+"-exact", ExactKey)
		indexes[p] = idx
		s.RegisterProperty(name, idx)
	}
	s.AddInstance("A")
	var wg sync.WaitGroup
	for p := 0; p < props; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			name := fmt.Sprintf("prop-%d", p)
			for r := 0; r < 100; r++ {
				if err := s.Write("A", name, Of(fmt.Sprintf("p%02d-v%03d", p, r))); err != nil {
					t.Errorf("write failed: %v", err)
					return
				}
			}
		}(p)
	}
	wg.Wait()
	// 每个属性的最终值与索引一致。
	for p := 0; p < props; p++ {
		name := fmt.Sprintf("prop-%d", p)
		v, err := s.Get("A", name)
		if err != nil || !v.Present {
			t.Fatalf("%s final value=%+v err=%v", name, v, err)
		}
		got, err := s.QueryByValue(name, v)
		if err != nil || len(got) != 1 || got[0] != "A" {
			t.Fatalf("%s index mismatch for %+v: %v", name, v, got)
		}
		if indexes[p].Size() != 1 {
			t.Fatalf("%s index size=%d, want 1", name, indexes[p].Size())
		}
	}
	if s.Clock() != props*100 {
		t.Fatalf("clock=%d, want %d", s.Clock(), props*100)
	}
}

// TestConcurrentWriteAndQuery 并发写入与查询：查询不死锁、不报错，
// 且返回的每个实例都存在（查询内部已按当前值校验）。
func TestConcurrentWriteAndQuery(t *testing.T) {
	s, _, _, _ := newTestStore(NewBufferLogger())
	for i := 0; i < 20; i++ {
		s.AddInstance(fmt.Sprintf("inst-%02d", i))
	}
	values := []Value{Of("red"), Of("green"), Of("blue")}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("inst-%02d", i)
			for r := 0; r < 100; r++ {
				if err := s.Write(id, "status", values[r%3]); err != nil {
					t.Errorf("write failed: %v", err)
					return
				}
			}
		}(i)
	}
	for q := 0; q < 8; q++ {
		wg.Add(1)
		go func(q int) {
			defer wg.Done()
			for r := 0; r < 200; r++ {
				got, err := s.QueryByValue("status", values[(q+r)%3])
				if err != nil {
					t.Errorf("query failed: %v", err)
					return
				}
				for _, id := range got {
					if _, err := s.Get(id, "status"); err != nil {
						t.Errorf("query returned unknown instance %s", id)
						return
					}
				}
			}
		}(q)
	}
	wg.Wait()
}

// TestConcurrentWriteDelete 并发写入与删除同一实例：删除生效后写入报实例不存在，
// 最终索引无残留。
func TestConcurrentWriteDelete(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		s, _, exact, prefix := newTestStore(NewBufferLogger())
		s.AddInstance("A")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = s.Write("A", "status", Of("v"))
			}
		}()
		go func() {
			defer wg.Done()
			_ = s.DeleteInstance("A")
		}()
		wg.Wait()
		if _, err := s.Get("A", "status"); err == nil {
			t.Fatalf("trial %d: instance should be deleted", trial)
		}
		if exact.Size() != 0 || prefix.Size() != 0 {
			t.Fatalf("trial %d: residual entries %d/%d", trial, exact.Size(), prefix.Size())
		}
	}
}
