// Command demo demonstrates the causal delivery buffer.
package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/cbuf"
	"ontology/vc"
	_ "unsafe"
)

// White-box read of the unexported check counter via go:linkname to an
// unexported cbuf function; never through an exported API.
//
//go:linkname probeCount ontology/cbuf.probeCount
func probeCount(*cbuf.Buffer) int64

type Msg = api.Msg

func M(from int, v ...int64) Msg { return Msg{From: from, V: v} }
func line(tag string, ok bool) {
	fmt.Printf("%s: %s\n", tag, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func main() {
	// 1-3. 第三节七次到达：每步交付与 V_local、第 6 步级联顺序、第 7 步重复。
	a, b := M(0, 1, 0, 0), M(1, 1, 1, 0)
	c, d := M(0, 2, 0, 0), M(2, 1, 1, 1)
	e, f := M(1, 2, 2, 0), M(2, 2, 2, 2)
	buf, _ := api.New(3, 8)
	steps := []struct {
		m    Msg
		want []Msg
		lv   []int64
	}{
		{e, nil, []int64{0, 0, 0}}, {d, nil, []int64{0, 0, 0}}, {b, nil, []int64{0, 0, 0}},
		{c, nil, []int64{0, 0, 0}}, {f, nil, []int64{0, 0, 0}},
		{a, []Msg{a, c, b, e, d, f}, []int64{2, 2, 2}}, {b, nil, []int64{2, 2, 2}},
	}
	stepOK, cascadeOK := true, true
	for i, s := range steps {
		got, err := buf.Receive(s.m)
		stepOK = stepOK && err == nil && reflect.DeepEqual(got, s.want) && reflect.DeepEqual(buf.Local(), s.lv)
		if i == 5 {
			cascadeOK = reflect.DeepEqual(got, steps[5].want)
		}
	}
	dupOK := buf.Dups() == 1 && len(buf.Buffered()) == 0 && reflect.DeepEqual(buf.Local(), []int64{2, 2, 2})
	line("stepwise-deliveries+V_local", stepOK)
	line("step6-cascade-order", cascadeOK)
	line("step7-duplicate-dropped", dupOK)

	// 4. 随机到达顺序下与朴素参照一致（SelfCheck 内置朴素整表扫描参照）。
	check, _ := api.New(3, 8)
	line("randomized-vs-naive-reference", check.SelfCheck() == nil)

	// 5-6. 四类互不相同的可判定错误；被拒后状态不变。
	errs := map[error]bool{}
	_, p := api.New(0, 4)
	errs[p] = true
	_, p = api.New(2, -1)
	errs[p] = true
	b2, _ := api.New(2, 4)
	snap := func() string { return fmt.Sprint(b2.Local(), len(b2.Buffered()), len(b2.Delivered()), b2.Dups()) }
	before := snap()
	_, e1 := b2.Receive(M(2, 1, 0)) // sender out of range
	_, e2 := b2.Receive(M(0, 1))    // vector length
	_, e3 := b2.Receive(M(0, 0, 0)) // own seq < 1
	b0, _ := api.New(1, 0)
	_, e4 := b0.Receive(M(0, 2)) // needs buffering but full
	errs[e1], errs[e2], errs[e3], errs[e4] = true, true, true, true
	line("four-distinct-sentinel-errors", len(errs) == 4 && e1 == api.ErrBadSender &&
		e2 == api.ErrBadVector && e3 == api.ErrBadVector && e4 == api.ErrBufferFull)
	line("rejection-leaves-no-trace", snap() == before &&
		fmt.Sprint(b0.Local(), len(b0.Buffered()), len(b0.Delivered()), b0.Dups()) == "[0] 0 0 0")

	// 7. 大 m 下检查条数不随 m 增长；级联时界为 (交付数+1)*n+常数。
	complexOK := true
	for _, m := range []int{100, 1000, 10000} {
		cb, _ := cbuf.New(2, m+1)
		for seq := int64(2); seq <= int64(m)+1; seq++ {
			if _, err := cb.Receive(M(1, 0, seq)); err != nil {
				complexOK = false
			}
		}
		cb.Receive(M(0, 1, 0))
		if probeCount(cb) > 4 { // 与 m 无关的小常数
			complexOK = false
		}
		got, _ := cb.Receive(M(1, 0, 1))
		if probeCount(cb) > int64((len(got)+1)*2+2) || len(got) != m+1 {
			complexOK = false
		}
	}
	line("check-count-not-linear-in-m", complexOK)

	// 8. 并发到达：闭合集 + 重复副本，全部交付、因果序成立、dups 准确。
	const n, total, extra, workers = 4, 40, 20, 8
	pool, loc := []Msg{}, make([]int64, n)
	for k := 0; k < total; k++ {
		j := k % n
		m := Msg{From: j, V: append([]int64(nil), loc...)}
		m.V[j]++
		loc[j]++
		pool = append(pool, m)
	}
	rng := rand.New(rand.NewSource(7))
	all := append([]Msg{}, pool...)
	for k := 0; k < extra; k++ {
		all = append(all, pool[rng.Intn(total)])
	}
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	pb, _ := api.New(n, len(all))
	ch := make(chan Msg)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range ch {
				pb.Receive(m)
			}
		}()
	}
	for _, m := range all {
		ch <- m
	}
	close(ch)
	wg.Wait()
	seen, ordered := make([]int64, n), true
	for _, m := range pb.Delivered() {
		if !vc.Deliverable(m, seen) {
			ordered = false
		}
		seen[m.From]++
	}
	sum := int64(0)
	for _, x := range pb.Local() {
		sum += x
	}
	line("concurrent-delivery-causal", len(pb.Delivered()) == total &&
		len(pb.Buffered()) == 0 && sum == total && pb.Dups() == extra && ordered)
}
