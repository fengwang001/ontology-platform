package reclaim

import (
	"testing"

	"ontology/snapshot"
	"ontology/txid"
)

func setup() (*txid.Counter, *snapshot.Manager, *Reclaimer) {
	src := txid.NewCounter()
	m := snapshot.NewManager(src, 0)
	return src, m, New(m, src)
}

func TestCollectAllWithoutSnapshots(t *testing.T) {
	src, _, r := setup()
	for i := 0; i < 5; i++ {
		if _, err := src.Next(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 4; i++ {
		r.Add(Candidate{Key: "k", Commit: txid.ID(i), Shadow: txid.ID(i + 1)})
	}
	got := r.Collect()
	if len(got) != 4 {
		t.Fatalf("无活跃快照应回收全部候选，得到 %d", len(got))
	}
	if r.Examined() != 4 {
		t.Fatalf("考察数应为 4，得到 %d", r.Examined())
	}
}

func TestHorizonBoundsCollect(t *testing.T) {
	src, m, r := setup()
	w, _ := m.BeginWriter()
	m.EndWriter(w)
	s, err := m.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := src.Next(); err != nil {
			t.Fatal(err)
		}
	}
	r.Add(Candidate{Key: "a", Commit: 1, Shadow: 2})
	r.Add(Candidate{Key: "b", Commit: 1, Shadow: 3})
	r.Add(Candidate{Key: "c", Commit: 1, Shadow: 10})
	if got := r.Collect(); len(got) != 0 {
		t.Fatalf("水位为 2 时无候选就绪，得到 %d", len(got))
	}
	m.Release(s)
	if got := r.Collect(); len(got) != 3 {
		t.Fatalf("释放快照后应全部就绪，得到 %d", len(got))
	}
	if r.Examined() != 3 {
		t.Fatalf("考察数应为 3，得到 %d", r.Examined())
	}
}

func TestWaterLevelMonotonic(t *testing.T) {
	src, m, r := setup()
	if _, err := src.Next(); err != nil {
		t.Fatal(err)
	}
	r.Collect()
	w1 := r.WaterLevel()
	s, _ := m.Begin()
	r.Collect()
	w2 := r.WaterLevel()
	m.Release(s)
	r.Collect()
	w3 := r.WaterLevel()
	if !(w1 <= w2 && w2 <= w3) {
		t.Fatalf("水位必须只升不降: %v %v %v", w1, w2, w3)
	}
}

func TestExaminedIndependentOfPending(t *testing.T) {
	src, m, r := setup()
	if _, err := src.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Next(); err != nil {
		t.Fatal(err)
	}
	s, err := m.Begin() // point=3，水位被钉在 3
	if err != nil {
		t.Fatal(err)
	}
	defer m.Release(s)
	r.Add(Candidate{Key: "hot", Commit: 1, Shadow: 2})
	for i := 0; i < 100000; i++ {
		r.Add(Candidate{Key: "cold", Commit: 1, Shadow: txid.ID(1000 + i)})
	}
	got := r.Collect()
	if len(got) != 1 || r.Examined() != 1 {
		t.Fatalf("考察数应只等于就绪候选数 1，得到 %d/%d", len(got), r.Examined())
	}
}
