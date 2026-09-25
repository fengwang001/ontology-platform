// Command demo demonstrates read-your-writes session consistency.
package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/api"
	"ontology/kv"
	"ontology/ses"
)

func report(name string, ok bool, detail string) int {
	if ok {
		if detail != "" {
			name = name + " :: " + detail
		}
		fmt.Println("OK: " + name)
		return 0
	}
	fmt.Println("FAIL: " + name + " " + detail)
	return 1
}

func mapsEqual(a, b map[string]kv.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func main() {
	fails := 0

	// kv: per-key monotonic versions.
	mk := kv.NewMaster()
	v1, v2 := mk.Advance("k", "A"), mk.Advance("k", "B")
	ke, _ := mk.Replica.Get("k")
	fails += report("kv advance monotonic, AtLeast", v1 == 1 && v2 == 2 && ke.Val == "B" && ke.AtLeast(v2), "")

	// ses: routing cost constant + distinct empty-key error.
	_, emptyErr := ses.NewCluster().Write(ses.NewSession(), "", "x")
	fails += report("ses constant routing cost; empty key rejected",
		ses.VerifyRouteCheckCost() == nil && errors.Is(emptyErr, ses.ErrEmptyKey), "")

	// Seven-step walk-through from NOTES.md.
	st := api.New()
	s := st.Open()
	var b strings.Builder
	expect := map[string]kv.Entry{}
	put := func(op, rd string) {
		a, x, y := st.Snap()
		fmt.Fprintf(&b, "%s R0=%v R1=%v R2=%v wv=%d read=%s\n", op, a["k"], x["k"], y["k"], s.WriteVersion("k"), rd)
	}
	w := func(val string) {
		ver, _ := st.Write(s, "k", val)
		expect["k"] = kv.Entry{Val: val, Ver: ver}
	}
	rd := func() string {
		val, ver, _ := st.Read(s, "k")
		return fmt.Sprintf("(%s,%d)", val, ver)
	}
	w("A")
	put("1 Write A", "-")
	put("2 Read", rd())
	_ = st.Sync(1)
	put("3 Sync1", "-")
	w("B")
	put("4 Write B", "-")
	_ = st.Sync(2)
	put("5 Sync2", "-")
	w("C")
	put("6 Write C", "-")
	put("7 Read", rd())
	steps := b.String()
	wants := []string{"R0={A 1}", "R1={ 0}", "read=(A,1)", "R1={A 1}", "R0={B 2}", "R2={B 2}", "R0={C 3}", "read=(C,3)"}
	okSteps := true
	for _, ww := range wants {
		okSteps = okSteps && strings.Contains(steps, ww)
	}
	oneline := strings.Join(strings.FieldsFunc(steps, func(r rune) bool { return r == '\n' }), " | ")
	fails += report("seven-step R0/R1/R2+read (wv shown)", okSteps, oneline)

	// View equals a naive reference: one replica with every write applied.
	fails += report("View matches naive reference", mapsEqual(st.View(), expect), "")

	// Three distinct decidable errors; rejected ops leave no trace.
	before := st.View()
	_, eW := st.Write(s, "", "z")
	_, _, eR := st.Read(s, "")
	eS := st.Sync(9)
	s2 := st.Open()
	st.Close(s2)
	_, eCW := st.Write(s2, "q", "1")
	_, _, eCR := st.Read(s2, "q")
	distinct := errors.Is(eW, api.ErrEmptyKey) && errors.Is(eR, api.ErrEmptyKey) &&
		errors.Is(eS, api.ErrSyncIndex) && errors.Is(eCW, api.ErrClosed) && errors.Is(eCR, api.ErrClosed)
	fails += report("three distinct sentinel errors; state unchanged", distinct && mapsEqual(before, st.View()), "")

	// Constant routing cost at large m.
	fails += report("large-m check count constant", ses.VerifyRouteCheckCost() == nil, "")

	// Concurrent readers see field-by-field identical Views.
	const n = 32
	views := make([]map[string]kv.Entry, n)
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func(i int) { views[i] = st.View(); done <- struct{}{} }(i)
	}
	same := true
	for i := 0; i < n; i++ {
		<-done
	}
	for i := 1; i < n; i++ {
		same = same && mapsEqual(views[0], views[i])
	}
	fails += report("concurrent readers identical View", same, "")

	fails += report("SelfCheck passes", st.SelfCheck() == nil, "")
	if fails > 0 {
		fmt.Println("DEMO FAILED")
	}
}
