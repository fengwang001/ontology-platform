package ontology

import (
	"reflect"
	"testing"
)

// feed 向抽样器依次加入 n 个元素，权重按确定规律变化。
func feed(s *Sampler, n int) {
	for i := 0; i < n; i++ {
		w := float64(i%7 + 1)
		if err := s.Add(i, w); err != nil {
			panic(err)
		}
	}
}

// 同一种子、同一输入序列，两次抽样结果必须逐元素一致，
// 且消耗的随机数个数相同。
func TestSameSeedReproducible(t *testing.T) {
	const seed = 42
	s1, err := NewSampler(10, seed)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewSampler(10, seed)
	if err != nil {
		t.Fatal(err)
	}
	feed(s1, 1000)
	feed(s2, 1000)

	got1, got2 := s1.Sample(), s2.Sample()
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("same seed produced different samples:\n%v\n%v", got1, got2)
	}
	if s1.RandConsumed() != s2.RandConsumed() {
		t.Fatalf("rand consumed differ: %d vs %d",
			s1.RandConsumed(), s2.RandConsumed())
	}
	if s1.RandConsumed() != 1000 {
		t.Fatalf("expected 1000 randoms consumed, got %d", s1.RandConsumed())
	}
}

// 不同种子在同一输入上必须能产出不同结果：
// 给一组种子，断言至少有两个结果不同。
func TestDifferentSeedsDiffer(t *testing.T) {
	var samples [][]any
	for seed := uint64(1); seed <= 20; seed++ {
		s, err := NewSampler(5, seed)
		if err != nil {
			t.Fatal(err)
		}
		feed(s, 200)
		samples = append(samples, s.Sample())
	}
	distinct := 0
	for i := 1; i < len(samples); i++ {
		if !reflect.DeepEqual(samples[0], samples[i]) {
			distinct++
		}
	}
	if distinct == 0 {
		t.Fatal("all seeds produced identical samples")
	}
}

// Sample 是幂等的：没有新增元素时多次调用结果一致，
// 且不消耗随机数；返回的切片与内部状态互不影响。
func TestSampleIdempotentAndIsolated(t *testing.T) {
	s, err := NewSampler(8, 7)
	if err != nil {
		t.Fatal(err)
	}
	feed(s, 500)

	before := s.RandConsumed()
	a := s.Sample()
	b := s.Sample()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two Samples differ:\n%v\n%v", a, b)
	}
	if s.RandConsumed() != before {
		t.Fatalf("Sample consumed randoms: %d -> %d", before, s.RandConsumed())
	}

	// 修改返回的切片不得影响内部状态。
	for i := range a {
		a[i] = "corrupted"
	}
	c := s.Sample()
	if !reflect.DeepEqual(b, c) {
		t.Fatalf("mutating returned slice affected sampler:\n%v\n%v", b, c)
	}
}
