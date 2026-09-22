package snapshot

import (
	"errors"
	"testing"

	"ontology/txid"
)

func TestBeginPointIsCurrentPlusOne(t *testing.T) {
	src := txid.NewCounter()
	m := NewManager(src, 0)
	s1, err := m.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if s1.Point() != 1 {
		t.Fatalf("无任何分配时快照点应为 1，得到 %v", s1.Point())
	}
	if _, err := m.BeginWriter(); err != nil {
		t.Fatal(err)
	}
	s2, _ := m.Begin()
	if s2.Point() != 2 {
		t.Fatalf("快照点应为 Current+1=2，得到 %v", s2.Point())
	}
}

func TestActiveSetCapturedAtBegin(t *testing.T) {
	src := txid.NewCounter()
	m := NewManager(src, 0)
	w1, _ := m.BeginWriter()
	s, _ := m.Begin()
	w2, _ := m.BeginWriter()
	if !s.IsActive(w1) {
		t.Fatalf("建立前已开始的事务应在活跃集合中")
	}
	if s.IsActive(w2) {
		t.Fatalf("建立后才开始的事务不得在活跃集合中")
	}
	m.EndWriter(w1)
	if !s.IsActive(w1) {
		t.Fatalf("活跃集合在建立时冻结，注销不影响旧快照")
	}
}

func TestLimit(t *testing.T) {
	m := NewManager(txid.NewCounter(), 1)
	if _, err := m.Begin(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Begin(); !errors.Is(err, ErrLimit) {
		t.Fatalf("超限应返回 ErrLimit，得到 %v", err)
	}
	if m.Count() != 1 {
		t.Fatalf("超限拒绝不得改变活跃数")
	}
}

func TestHorizon(t *testing.T) {
	src := txid.NewCounter()
	m := NewManager(src, 0)
	if _, ok := m.Horizon(); ok {
		t.Fatalf("无活跃快照时视野应无界")
	}
	w1, _ := m.BeginWriter() // id=1
	if _, err := m.BeginWriter(); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Begin() // point=3, active={1,2}
	m.EndWriter(w1)
	h, ok := m.Horizon()
	if !ok || h != 1 {
		t.Fatalf("视野应取活跃集合最小值 1，得到 %v %v", h, ok)
	}
	m.Release(s)
	if _, ok := m.Horizon(); ok {
		t.Fatalf("释放后视野应回到无界")
	}
}
