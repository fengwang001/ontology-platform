package main

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"ontology/api"
	"ontology/gtid"
)

const uuidA, uuidB, uuidU = "3e11fa47-71ca-11e1-9e33-c80aa9429562", "8a94f357-aab4-11df-86ab-c80aa9429562", "00000000-0000-0000-0000-000000000075"

var fails int

func ok(name string, cond bool) {
	if !cond {
		fails++
	}
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[cond] + name)
}

func naive(m map[string]map[int64]bool) string {
	var ps []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		ns := slices.Sorted(maps.Keys(m[k]))
		var sg []string
		for i := 0; i < len(ns); i++ {
			j := i
			for j+1 < len(ns) && ns[j+1] == ns[j]+1 {
				j++
			}
			v := strconv.FormatInt(ns[i], 10)
			if ns[j] != ns[i] {
				v += "-" + strconv.FormatInt(ns[j], 10)
			}
			sg, i = append(sg, v), j
		}
		ps = append(ps, k+":"+strings.Join(sg, ":"))
	}
	return strings.Join(ps, ",")
}

func main() {
	rep := strings.NewReplacer("A", uuidA, "B", uuidB)
	t, _ := api.New(4)
	steps := []struct {
		tx, want string
		missing  bool
		err      error
	}{
		{"A:1-5:7", "A:1-5:7", false, nil}, {"A:6", "A:1-7", false, nil},
		{"B:20-25:10-12,A:9-12:8", "A:1-12,B:10-12:20-25", false, nil},
		{"A:13-14,B:5-3", "A:1-12,B:10-12:20-25", false, gtid.ErrRange},
		{"B:13-14:26", "A:1-12,B:10-14:20-26", false, nil},
		{"B:18-19,B:16", "A:1-12,B:10-14:16:18-26", false, nil},
		{"B:1-30,A:1-15", "A:13-15,B:1-9:15:17:27-30", true, nil},
		{"A:12-13", "A:13", true, nil},
	}
	eightOK := true
	for _, s := range steps {
		var got string
		err := error(nil)
		if s.missing {
			got, err = t.Missing(rep.Replace(s.tx))
		} else {
			err = t.Apply(rep.Replace(s.tx))
			got = t.Executed()
		}
		eightOK = eightOK && errors.Is(err, s.err) && got == rep.Replace(s.want)
	}
	ok("eight prescribed steps (step4 rejected; gap texts)", eightOK)
	t2, _ := api.New(50)
	for _, tx := range []string{uuidU + ":1-5:7", uuidU + ":6", uuidU + ":5:4-7", strings.ToUpper(uuidA) + ":1-7:8:9-12"} {
		_ = t2.Apply(tx)
	}
	ok("adjacency merge & shuffled/dup/uppercase canonical", t2.Executed() == uuidU+":1-7,"+uuidA+":1-12")
	t3, _ := api.New(10)
	_ = t3.Apply(uuidU + ":3-5:9")
	g3, _ := t3.Missing(uuidU + ":1-10")
	ok("closed-interval diff endpoints [s,a-1]/[b+1,e]", g3 == uuidU+":1-2:6-8:10")
	rng := rand.New(rand.NewSource(42))
	t4, _ := api.New(500)
	us := []string{uuidA, uuidB, "cccccccc-3333-3333-3333-333333333333"}
	em := map[string]map[int64]bool{us[0]: {}, us[1]: {}, us[2]: {}}
	for range 200 {
		u, n := us[rng.Intn(3)], int64(rng.Intn(200)+1)
		_ = t4.Apply(fmt.Sprintf("%s:%d", u, n))
		em[u][n] = true
	}
	srcs := []string{}
	sm := map[string]map[int64]bool{us[0]: {}, us[1]: {}, us[2]: {}}
	for _, u := range us {
		for n := int64(1); n <= 200; n++ {
			if rng.Intn(2) == 0 {
				srcs = append(srcs, fmt.Sprintf("%s:%d", u, n))
				if !em[u][n] {
					sm[u][n] = true
				}
			}
		}
	}
	g4, _ := t4.Missing(strings.Join(srcs, ","))
	ok("random sets match naive element-wise union/diff", t4.Executed() == naive(em) && g4 == naive(sm))
	bad := map[string]error{uuidU + ": 1": gtid.ErrSyntax, uuidU + ":1::3": gtid.ErrSyntax, "nope:1": gtid.ErrUUID, uuidU + ":0": gtid.ErrRange, uuidU + ":9-2": gtid.ErrRange}
	seen, errOK := map[error]bool{}, true
	for tx, want := range bad {
		seen[want] = true
		errOK = errOK && errors.Is(t4.Apply(tx), want)
	}
	t5, _ := api.New(1)
	_ = t5.Apply(uuidU + ":1")
	errOK = errOK && errors.Is(t5.Apply(uuidU+":3"), gtid.ErrTooMany)
	ok("four distinct decidable errors", errOK && len(seen) == 3)
	ok("rejected apply leaves executed set unchanged (step4 A:13-14 absent)",
		t.Executed() == uuidA+":1-12,"+uuidB+":10-14:16:18-26")
	lmOK := true
	for _, m := range []int{100, 1000, 10000} {
		lm, _ := api.New(m)
		ev := make([]string, m)
		for i := range ev {
			ev[i] = strconv.Itoa(2 * (i + 1))
		}
		_ = lm.Apply(uuidU + ":" + strings.Join(ev, ":"))
		g, err := lm.Missing(uuidU + ":1-3")
		lmOK = lmOK && err == nil && g == uuidU+":1:3"
	}
	ok("large-m gap located for m=100..10000 (bound asserted in ivl_test)", lmOK)
	var wg sync.WaitGroup
	tc, _ := api.New(500)
	for _, g := range rng.Perm(50) {
		wg.Add(1)
		go func(g int) { defer wg.Done(); _ = tc.Apply(fmt.Sprintf("%s:%d-%d", uuidU, 10*g+1, 10*g+10)) }(g)
	}
	wg.Wait()
	ok("concurrent disjoint applies equal one-shot union", tc.Executed() == uuidU+":1-500")
	ok("SelfCheck passes", t.SelfCheck() == nil && t4.SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}
