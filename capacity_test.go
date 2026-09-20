package ontology

import "testing"

// 百万元素流上，任意时刻水塘大小不得超过 k，
// 且结束后水塘恰好有 k 个元素。
func TestMillionStreamBoundedMemory(t *testing.T) {
	const k = 100
	const n = 1_000_000
	s, err := NewSampler(k, 2024)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := s.Add(i, float64(i%5+1)); err != nil {
			t.Fatal(err)
		}
		if i%4096 == 0 && s.Size() > k {
			t.Fatalf("at i=%d Size()=%d exceeds k=%d", i, s.Size(), k)
		}
	}
	if s.Size() != k {
		t.Fatalf("final Size()=%d, want %d", s.Size(), k)
	}
	if s.Total() != n {
		t.Fatalf("Total()=%d, want %d", s.Total(), n)
	}
	if s.RandConsumed() != n {
		t.Fatalf("RandConsumed()=%d, want %d", s.RandConsumed(), n)
	}
	if len(s.Sample()) != k {
		t.Fatalf("len(Sample())=%d, want %d", len(s.Sample()), k)
	}
}
