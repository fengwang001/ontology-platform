package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

var pass, total int

func check(label string, ok bool) {
	total++
	if ok {
		pass++
		fmt.Println("OK", label)
	} else {
		fmt.Println("FAIL", label)
	}
}

func main() {
	ns := name.New([]string{"a"})
	ns.Lock()
	blocked := make(chan struct{})
	go func() {
		ns.Lock()
		defer ns.Unlock()
		close(blocked)
	}()
	select {
	case <-blocked:
	case <-time.After(20 * time.Millisecond):
	}
	ns.Unlock()
	select {
	case <-blocked:
		check("name: concurrent write blocked during batch", true)
	case <-time.After(time.Second):
		check("name: concurrent write blocked during batch", false)
	}
	occ := make(map[string]struct{})
	for i := 0; i < cycle.TriesPerFamily; i++ {
		occ[cycle.Prefix0+itoa(i)] = struct{}{}
	}
	tmp, err := cycle.TempName(func(c string) bool { _, ok := occ[c]; return ok })
	check("cycle: temp name succeeds even when whole first family occupied",
		err == nil && tmp == cycle.Prefix1+"0")

	compile := func(init []string, reqs []plan.Req) ([]plan.Step, error) {
		n := name.New(init)
		n.Lock()
		defer n.Unlock()
		steps, _, err := plan.Compile(n, reqs)
		return steps, err
	}
	noOverwrite := func(init []string, steps []plan.Step) bool {
		n := name.New(init)
		n.Lock()
		defer n.Unlock()
		for _, s := range steps {
			if n.HasLocked(s.To) {
				return false
			}
			if err := n.RenameLocked(s.From, s.To, false); err != nil {
				return false
			}
		}
		return true
	}
	chain, err := compile([]string{"a", "b"}, []plan.Req{{"a", "b"}, {"b", "c"}})
	check("plan: {a->b,b->c} order b->c then a->b, no overwrite",
		err == nil && len(chain) == 2 && chain[0].From == "b" &&
			chain[1].From == "a" && noOverwrite([]string{"a", "b"}, chain))

	n3 := name.New([]string{"a", "b", "c"})
	n3.Lock()
	steps3, st3, err3 := plan.Compile(n3,
		[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}})
	n3.Unlock()
	check("plan: 3-cycle uses one temp, every target absent",
		err3 == nil && st3.TempNames == 1 && noOverwrite([]string{"a", "b", "c"}, steps3))

	conflicts := []struct {
		label string
		init  []string
		reqs  []plan.Req
		want  error
	}{
		{"target exists", []string{"a", "b"}, []plan.Req{{"a", "b"}}, plan.ErrTargetExists},
		{"dup target", []string{"a", "b"}, []plan.Req{{"a", "c"}, {"b", "c"}}, plan.ErrDupTarget},
		{"dup source", []string{"a"}, []plan.Req{{"a", "b"}, {"a", "c"}}, plan.ErrDupSource},
		{"missing source", []string{"a"}, []plan.Req{{"x", "y"}}, plan.ErrMissingSource},
	}
	confOK := true
	for _, c := range conflicts {
		n := name.New(c.init)
		n.Lock()
		before := n.SnapshotLocked()
		_, _, gerr := plan.Compile(n, c.reqs)
		unchanged := name.Equal(n.SnapshotLocked(), before)
		n.Unlock()
		if gerr == nil || !errorsIs(gerr, c.want) || !unchanged {
			confOK = false
		}
	}
	check("plan: four conflict types detected before any mutation", confOK)

	var cinit []string
	var creqs []plan.Req
	for i := 0; i < 10; i++ {
		a, b := "a"+itoa(i), "b"+itoa(i)
		cinit = append(cinit, a, b)
		creqs = append(creqs, plan.Req{a, b}, plan.Req{b, a})
	}
	nc := name.New(cinit)
	nc.Lock()
	_, stc, errc := plan.Compile(nc, creqs)
	nc.Unlock()
	check("plan: temp count equals cycle count (10)", errc == nil && stc.TempNames == 10)

	var dinit []string
	var dreqs []plan.Req
	for i := 0; i < 6; i++ {
		a, b := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)
		dinit = append(dinit, a, b)
		dreqs = append(dreqs, plan.Req{a, b}, plan.Req{b, a})
	}
	detOK := true
	ref := ""
	for round := 0; round < 20 && detOK; round++ {
		sh := append([]plan.Req(nil), dreqs...)
		rng := rand.New(rand.NewSource(int64(round + 1)))
		rng.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		ss, err := compile(dinit, sh)
		if err != nil {
			detOK = false
			break
		}
		cur := stepsKey(ss)
		if round == 0 {
			ref = cur
		} else if cur != ref {
			detOK = false
		}
	}
	check("plan: shuffled construction order 20x gives identical steps", detOK)

	dir, _ := os.MkdirTemp("", "demo-rename-*")
	rbOK := true
	rbReqs := []plan.Req{{"a", "aa"}, {"b", "bb"}, {"c", "cc"},
		{"aa", "aaa"}, {"bb", "bbb"}}
	for _, k := range []int{1, 3, 5} {
		nr := name.New([]string{"a", "b", "c"})
		before := nr.Snapshot()
		path, _, ferr := apply.Exec(nr, rbReqs, dir, apply.FailAt(k))
		if ferr == nil || path != "" || !name.Equal(nr.Snapshot(), before) {
			rbOK = false
		}
	}
	check("apply: failure at k=1/middle/last rolls back fully", rbOK)

	nu := name.New([]string{"a", "b", "c"})
	beforeU := nu.Snapshot()
	logPath, _, errU := apply.Exec(nu,
		[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}}, dir, 0)
	undoOK := errU == nil && undoDemo(nu, logPath) &&
		name.Equal(nu.Snapshot(), beforeU)
	check("apply+undo: namespace element-identical after undo", undoOK)
	fmt.Printf("TOTAL %d/%d\n", pass, total)
}

// undoDemo 用与 apply 相同的帧编码做最小逆序撤销（截断分类由 undo 包负责）。
func undoDemo(ns *name.Namespace, path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < apply.HeaderLen+apply.TrailerLen {
		return false
	}
	type rec struct{ From, To string }
	var recs []rec
	pos := apply.HeaderLen
	for pos < len(data)-apply.TrailerLen {
		if data[pos] != 0x01 {
			return false
		}
		n := int(data[pos+1])<<8 | int(data[pos+2])
		var r struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(data[pos+3:pos+3+n], &r); err != nil {
			return false
		}
		recs = append(recs, rec{r.From, r.To})
		pos += 3 + n
	}
	ns.Lock()
	defer ns.Unlock()
	for i := len(recs) - 1; i >= 0; i-- {
		if err := ns.RenameLocked(recs[i].To, recs[i].From, true); err != nil {
			return false
		}
	}
	return true
}

func stepsKey(ss []plan.Step) string {
	out := ""
	for _, s := range ss {
		out += s.From + ">" + s.To + "|"
	}
	return out
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
