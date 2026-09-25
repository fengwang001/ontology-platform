// demo 顺序验证区间合并器的核心语义，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"sync"

	"ontology/check"
	"ontology/iv"
	"ontology/merge"
)

var passed, failed int

func report(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK", name)
		return
	}
	failed++
	fmt.Println("FAIL", name)
}

func main() {
	_, err := iv.New(2, 2)
	report("empty interval rejected", errors.Is(err, iv.ErrEmptyInterval))

	touch := merge.New()
	_ = touch.AddAll([]iv.Interval{iv.Must(1, 2), iv.Must(2, 3)})
	report("touching [1,2)+[2,3)=[1,3)", slices.Equal(touch.Ranges(), []iv.Interval{iv.Must(1, 3)}))

	disjoint := merge.New()
	_ = disjoint.AddAll([]iv.Interval{iv.Must(5, 6), iv.Must(1, 2)})
	report("ranges sorted and disjoint", slices.Equal(disjoint.Ranges(), []iv.Interval{iv.Must(1, 2), iv.Must(5, 6)}))

	r := rand.New(rand.NewPCG(1, 0))
	in := make([]iv.Interval, 10000)
	for i := range in {
		in[i] = iv.Must(r.IntN(100), 101+r.IntN(100))
	}
	var ref check.Naive
	for _, v := range in {
		ref.Add(v)
	}
	m := merge.New()
	_ = m.AddAll(in)
	report("coverage equals naive reference", slices.Equal(m.Ranges(), ref.Ranges()))

	r.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
	shuffled := merge.New()
	_ = shuffled.AddAll(in)
	report("order independent", slices.Equal(shuffled.Ranges(), ref.Ranges()))

	report("merge never grows", len(m.Ranges()) <= m.Total() && m.Total() == 10000)

	conc := merge.New()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Go(func() {
			for i := g; i < len(in); i += 16 {
				_ = conc.Add(in[i])
			}
		})
	}
	wg.Wait()
	report("concurrent == sequential", slices.Equal(conc.Ranges(), ref.Ranges()))

	fmt.Printf("OK total %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
