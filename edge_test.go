package ontology

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

// k <= 0 必须返回可判定的错误而不是 panic。
func TestNonPositiveCapacity(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		s, err := NewSampler(k, 1)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("k=%d: expected ErrInvalidCapacity, got %v", k, err)
		}
		if s != nil {
			t.Fatalf("k=%d: expected nil sampler", k)
		}
	}
}

// 流长度小于 k 时返回全部元素，顺序为到达顺序。
func TestStreamShorterThanK(t *testing.T) {
	s, err := NewSampler(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := s.Add(i, 1); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Sample()
	want := []any{0, 1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if s.Size() != 4 {
		t.Fatalf("Size()=%d, want 4", s.Size())
	}
}

// 流长度恰好等于 k 时返回全部元素。
func TestStreamEqualsK(t *testing.T) {
	s, err := NewSampler(5, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.Add(i, 2); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Sample()
	want := []any{0, 1, 2, 3, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// k=1 是合法常用情形：水塘始终只留一个元素。
func TestCapacityOne(t *testing.T) {
	s, err := NewSampler(1, 9)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if err := s.Add(i, 1); err != nil {
			t.Fatal(err)
		}
		if s.Size() > 1 {
			t.Fatalf("Size()=%d exceeds k=1", s.Size())
		}
	}
	got := s.Sample()
	if len(got) != 1 {
		t.Fatalf("len(Sample())=%d, want 1", len(got))
	}
	v, ok := got[0].(int)
	if !ok || v < 0 || v >= 1000 {
		t.Fatalf("unexpected sample %v", got[0])
	}
}

// 权重为 0、负数、非整数、NaN、Inf 必须返回可判定的错误，
// 且该元素不得进入水塘；被拒绝数与接受数分别可读。
func TestInvalidWeightsRejected(t *testing.T) {
	s, err := NewSampler(4, 5)
	if err != nil {
		t.Fatal(err)
	}
	bad := []float64{0, -1, -2.5, 1.5, 0.1, math.NaN(), math.Inf(1)}
	for _, w := range bad {
		if err := s.Add("bad", w); !errors.Is(err, ErrInvalidWeight) {
			t.Fatalf("weight %v: expected ErrInvalidWeight, got %v", w, err)
		}
	}
	if s.Rejected() != uint64(len(bad)) {
		t.Fatalf("Rejected()=%d, want %d", s.Rejected(), len(bad))
	}
	if s.Total() != 0 || s.Size() != 0 {
		t.Fatalf("invalid items entered reservoir: total=%d size=%d",
			s.Total(), s.Size())
	}
	if len(s.Sample()) != 0 {
		t.Fatal("reservoir should be empty")
	}

	// 合法权重仍被接受，计数互不影响。
	if err := s.Add("good", 3); err != nil {
		t.Fatal(err)
	}
	if s.Total() != 1 || s.Rejected() != uint64(len(bad)) {
		t.Fatalf("Total()=%d Rejected()=%d", s.Total(), s.Rejected())
	}
}
