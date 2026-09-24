package slide_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/mono"
	"ontology/slide"
)

func TestErrorsDistinct(t *testing.T) {
	var q mono.Queue
	_ = q.Push(0, 1)
	_, eZero := slide.New(0)
	_, eNeg := slide.New(-3)
	_, eWide := slide.Maxes([]int{1, 2}, 3)
	eIdx := q.Push(0, 2)
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"w 为 0", eZero, slide.ErrBadWidth},
		{"w 为负", eNeg, slide.ErrBadWidth},
		{"w 大于序列长度", eWide, slide.ErrWindowTooWide},
		{"下标非递增", eIdx, mono.ErrBadIndex},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: 得到 %v", c.name, c.err)
		}
		for _, other := range cases {
			if other.want != c.want && errors.Is(c.err, other.want) {
				t.Errorf("%s 与其他哨兵错误不互异", c.name)
			}
		}
	}
	s, _ := slide.New(2) // 被拒操作之后滑窗器仍可正常使用
	s.Feed(5)
	s.Feed(4)
	if m := s.Feed(6); m != 6 || s.SelfCheck() != nil {
		t.Fatal("滑窗器在被拒操作后状态异常")
	}
}

func TestConcurrentMaxes(t *testing.T) {
	seq := shapes(2000)[1].seq
	gold, err := slide.Maxes(seq, 23)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := slide.New(23)
	for _, v := range seq[:100] {
		s.Feed(v)
	}
	const g = 16
	start := make(chan struct{})
	res := make([][]int, g)
	errs := make([]error, g)
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i], _ = slide.Maxes(seq, 23)
			errs[i] = s.SelfCheck()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range res {
		if errs[i] != nil || !slices.Equal(res[i], gold) {
			t.Fatalf("goroutine %d 结果不一致", i)
		}
	}
}
