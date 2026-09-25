package check_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/check"
	"ontology/mlfq"
)

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestErrors(t *testing.T) {
	s := mlfq.New(mlfq.Config{Levels: 1, Quotas: []int{1}, Boost: 9, MaxJobs: 1})
	s.Submit(1, 5)
	for _, c := range []struct{ got, want error }{
		{s.Submit(1, 9), mlfq.ErrDuplicate},
		{s.Yield(9), mlfq.ErrUnknown},
		{s.Submit(2, 9), mlfq.ErrFull},
	} {
		eq(t, errors.Is(c.got, c.want), true)
	}
	eq(t, s.Stats().Active, 1)
}

func TestGaming(t *testing.T) {
	ml := mlfq.New(mlfq.Config{Levels: 2, Quotas: []int{4, 99}, Boost: 1000, MaxJobs: 2})
	ml.Submit(1, 100)
	ml.Submit(2, 100)
	q, used := [2][]int{{1, 2}}, map[int]int{}
	wrong := func() (int, bool) { // 错误对照：每次让出都把配额账清零
		l := 1 - min(len(q[0]), 1)
		id := q[l][0]
		used[id]++
		if used[id] == 4 {
			q[l] = q[l][1:]
			q[1] = append(q[1], id)
		}
		return id, true
	}
	yieldWrong := func() {
		q[0] = append(q[0][1:], 1)
		used[1] = 0
	}
	cases := []struct {
		name  string
		step  func() (int, bool)
		yield func()
		want  []int
	}{
		{"cumulative", ml.Step, func() { ml.Yield(1) }, []int{1, 1, 1, 2, 2, 2, 2, 1, 2}},
		{"reset-on-yield", wrong, yieldWrong, []int{1, 1, 1, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1}},
	}
	for _, c := range cases {
		runs := 0
		for i, w := range c.want {
			id, _ := c.step()
			if id == 1 {
				runs++
				if runs%3 == 0 {
					c.yield()
				}
			}
			if got := id; got != w {
				t.Errorf("%s step %d: got %d want %d", c.name, i, got, w)
			}
		}
	}
}

func TestRandomRef(t *testing.T) {
	c := mlfq.Config{Levels: 3, Quotas: []int{3, 5, 9}, Boost: 17, MaxJobs: 12}
	s, n := mlfq.New(c), check.New(c)
	rng, next := rand.New(rand.NewSource(42)), 0
	for i := 0; i < 500; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2:
			next++
			eq(t, s.Submit(next, 1+next%9), n.Submit(next, 1+next%9))
		case 3:
			eq(t, s.Yield(next), n.Yield(next))
		default:
			g, gok := s.Step()
			w, wok := n.Step()
			eq(t, g, w)
			eq(t, gok, wok)
		}
	}
	eq(t, s.Stats().Completed, n.Done())
}

func TestBounds(t *testing.T) {
	c := mlfq.Config{Levels: 3, Quotas: []int{2, 3, 5}, Boost: 7, MaxJobs: 8}
	s := mlfq.New(c)
	last := map[int]int{}
	for id := 1; id <= 6; id++ {
		s.Submit(id, 10000)
	}
	maxGap, bound := 0, c.Boost+(c.MaxJobs-1)*c.Quotas[0]
	for tick := 1; tick <= 400; tick++ {
		id, _ := s.Step()
		if last[id] > 0 {
			maxGap = max(maxGap, tick-last[id])
		}
		last[id] = tick
	}
	eq(t, max(maxGap, bound), bound)
	big := mlfq.New(mlfq.Config{Levels: 3, Quotas: []int{2, 3, 5}, Boost: 7, MaxJobs: 10000})
	for i := 0; i < 10000; i++ {
		big.Submit(i, 1)
	}
	big.Step()
	eq(t, max(big.Stats().Checked, 4), 4)
}

func TestConcurrent(t *testing.T) {
	s := mlfq.New(mlfq.Config{Levels: 3, Quotas: []int{2, 4, 8}, Boost: 11, MaxJobs: 40})
	for w := 0; w < 4; w++ {
		go func() {
			for i := 0; i < 10; i++ {
				s.Submit(w*10+i, 5)
			}
		}()
	}
	ran := map[int]int{}
	for n := 0; n < 200; {
		if id, ok := s.Step(); ok {
			ran[id]++
			n++
		}
	}
	for i := 0; i < 40; i++ {
		eq(t, ran[i], 5)
	}
}
