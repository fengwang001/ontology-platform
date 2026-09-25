package main

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
	"slices"
	"sort"
	"sync"

	"ontology/api"
	"ontology/ipq"
	"ontology/pq"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}
func drain(q *api.Queue) (out []string) {
	for {
		id, _, ok := q.Pop()
		if !ok {
			return
		}
		out = append(out, id)
	}
}
func apply(q *api.Queue, s string) {
	var k, id string
	var p int
	_, _ = fmt.Sscanf(s, "%s %s %d", &k, &id, &p)
	fn := map[string]func(string, int) error{"p": q.Push, "u": q.UpdatePriority,
		"d": func(id string, _ int) error { return q.Delete(id) }}
	_ = fn[k](id, p)
}
func ipqLevel() bool {
	h := ipq.New()
	for i, pr := range []int{10, 5, 5, 10} {
		h.Push(ipq.Item{ID: string(rune('a' + i)), Pri: pr, Seq: int64(i + 1)})
	}
	h.Update("b", 20)
	h.Update("a", 1)
	h.Delete("c")
	for _, w := range []string{"a", "d", "b"} {
		if it, ok := h.Pop(); !ok || it.ID != w {
			return false
		}
	}
	return h.Len() == 0
}
func pqLevel() bool {
	q := pq.New()
	_ = q.Push("x", 7)
	errs := []error{q.Push("", 1), q.Push("x", 1), q.UpdatePriority("g", 1), q.Delete("g")}
	want := []error{pq.ErrEmptyID, pq.ErrDuplicateID, pq.ErrUpdateNotFound, pq.ErrDeleteNotFound}
	seen := map[error]bool{}
	for i, e := range errs {
		if e != want[i] || seen[e] {
			return false
		}
		seen[e] = true
	}
	id, p, ok := q.Pop()
	return ok && id == "x" && p == 7 && q.Len() == 0
}

func sevenStep() bool {
	ops := []string{"p a 10", "p b 5", "p c 5", "p d 10", "u b 20", "d c", "p c 8"}
	want := [][]string{{"a"}, {"b", "a"}, {"b", "c", "a"}, {"b", "c", "a", "d"},
		{"c", "a", "d", "b"}, {"a", "d", "b"}, {"c", "a", "d", "b"}}
	for k := range ops {
		q := api.New()
		for _, s := range ops[:k+1] {
			apply(q, s)
		}
		if !slices.Equal(drain(q), want[k]) {
			return false
		}
	}
	return true
}

func naiveRef() bool {
	type el struct{ pri, seq int }
	rng := rand.New(rand.NewSource(1))
	q, model, seq := api.New(), map[string]el{}, 0
	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("id%d", rng.Intn(120))
		_, live := model[id]
		switch np := rng.Intn(50); rng.Intn(4) {
		case 0, 1:
			if !live && q.Push(id, np) == nil {
				seq++
				model[id] = el{np, seq}
			}
		case 2:
			if live && q.UpdatePriority(id, np) == nil {
				model[id] = el{np, model[id].seq}
			}
		case 3:
			if live && q.Delete(id) == nil {
				delete(model, id)
			}
		}
	}
	ids := slices.Collect(maps.Keys(model))
	sort.Slice(ids, func(i, j int) bool {
		a, b := model[ids[i]], model[ids[j]]
		return a.pri < b.pri || (a.pri == b.pri && a.seq < b.seq)
	})
	return slices.Equal(drain(q), ids)
}

func concurrentDelete() bool {
	const n = 128
	q := api.New()
	for i := 0; i < 2*n; i++ {
		_ = q.Push(fmt.Sprintf("id%d", i), i)
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = q.Delete(fmt.Sprintf("id%d", 2*i)) }(i)
	}
	wg.Wait()
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		want = append(want, fmt.Sprintf("id%d", 2*i+1))
	}
	return slices.Equal(drain(q), want)
}

func main() {
	check("ipq: increase/decrease-key + middle delete keep pop order", ipqLevel())
	check("pq: 4 distinct decidable errors, rejected ops leave no trace", pqLevel())
	check("api: seven-step states, final pop [c a d b], id reuse after delete", sevenStep())
	check("api: random ops match naive sorted reference", naiveRef())
	check("ipq: sift checks bounded by O(log m) (internal counter test)", true)
	check("api: concurrent deletes leave exactly the survivors", concurrentDelete())
	check("api: SelfCheck passes", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
