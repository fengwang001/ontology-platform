package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/api"
	"ontology/ssi"
)

var fails int

func verdict(ok bool) string {
	if ok {
		return "OK"
	}
	fails++
	return "FAIL"
}
func trace() bool {
	e := ssi.NewEngine(map[string]string{"x": "10", "y": "10", "z": "5"})
	var tx [3]*ssi.Txn
	ops := strings.Split("0B,1B,0Rx,1Rx,0Ry,1Ry,2B,2Wz99,2Rz,0Wx-10,0C,2C,1Wy-10,1C", ",")
	want := strings.Split("sv0 R{} W{} i0o0|sv0 R{} W{} i0o0|sv0 R{x:10} W{} i0o0|sv0 R{x:10} W{} i0o0|sv0 R{x:10 y:10} W{} i0o0|sv0 R{x:10 y:10} W{} i0o0|sv0 R{} W{} i0o0|sv0 R{} W{z:99} i0o0|sv0 R{} W{z:99} i0o0|sv0 R{x:10 y:10} W{x:-10} i0o0|sv0 R{x:10 y:10} W{x:-10} i0o0|sv0 R{} W{z:99} i0o0|sv0 R{x:10 y:10} W{y:-10} i0o0|sv0 R{} W{} i1o1", "|")
	line := ""
	for n, op := range ops {
		i, a := int(op[0]-'0'), op[1]
		var err error
		switch a {
		case 'B':
			tx[i] = e.Begin()
		case 'R':
			tx[i].Read(op[2:])
		case 'W':
			tx[i].Write(op[2:3], op[3:])
		case 'C':
			err = tx[i].Commit()
		}
		c := e.Committed()
		good := tx[i].String() == want[n] && (err == nil || n == 13 && err == ssi.ErrConflict)
		if n == 10 || n == 11 || n == 13 {
			z := map[bool]string{true: "5", false: "99"}[n == 10]
			good = good && c["x"] == "-10" && c["y"] == "10" && c["z"] == z
		}
		line += verdict(good) + " "
	}
	fmt.Printf("14-step flags/state: %s\n", line)
	c := e.Committed()
	return c["x"] == "-10" && c["y"] == "10" && c["z"] == "99"
}
func reverse() bool {
	e := ssi.NewEngine(map[string]string{"x": "10", "y": "10", "z": "5"})
	t1, t2 := e.Begin(), e.Begin()
	func() { t1.Read("x"); t1.Read("y"); t2.Read("x"); t2.Read("y") }()
	if t2.Write("y", "-10") != nil || t2.Commit() != nil {
		return false
	}
	t1.Write("x", "-10")
	return t1.Commit() == ssi.ErrConflict && e.Committed()["x"] == "10" && e.Committed()["y"] == "-10"
}
func scale() bool {
	ok := true
	for _, m := range []int{100, 1000, 10000} {
		d := api.New()
		b := d.Begin()
		b.Write("k", "1")
		b.Commit()
		for j := 0; j < m; j++ {
			func() { t := d.Begin(); t.Read("k"); t.Commit() }()
		}
		w := d.Begin()
		w.Write("k", "2")
		if w.Commit() != nil || d.Committed()["k"] != "2" {
			ok = false
		}
	}
	return ok
}
func concurrent() bool {
	d := api.New()
	s := d.Begin()
	s.Write("a", "0")
	s.Write("b", "0")
	s.Commit()
	const N, L = 4, 200
	res := make(chan bool, N*L)
	for g := 0; g < N; g++ {
		go func() {
			for j := 0; j < L; j++ {
				t := d.Begin()
				va, _ := t.Read("a")
				vb, _ := t.Read("b")
				res <- va != vb || (va != "0" && va != "1")
				t.Commit()
			}
		}()
	}
	w := d.Begin()
	w.Write("a", "1")
	w.Write("b", "1")
	w.Commit()
	for j := 0; j < N*L; j++ {
		if <-res {
			return false
		}
	}
	return true
}
func main() {
	final := trace()
	rev := reverse()
	var nt *api.Txn
	_, e1 := nt.Read("k")
	d := api.New()
	t := d.Begin()
	_, e2 := t.Read("")
	e3 := t.Write("", "v")
	emptyTrace := len(d.Committed()) == 0
	t.Commit()
	_, e4 := t.Read("k")
	e5 := t.Commit()
	distinct := errors.Is(e1, ssi.ErrNoTxn) && errors.Is(e2, ssi.ErrEmptyKey) &&
		errors.Is(e4, ssi.ErrTxnFinished) && errors.Is(e5, ssi.ErrTxnFinished) && e1 != e2 && e2 != e4
	a := d.Begin()
	a.Write("q", "1")
	usable := a.Commit() == nil && d.Committed()["q"] == "1" && e3 == ssi.ErrEmptyKey
	checks := [][2]any{
		{"final x=-10 y=10 z=99, T2 rolled back", final},
		{"(reverse) T1 rolled back x=10 y=-10; serial replay", rev},
		{"read-your-writes T3 z=99; 3 distinct sentinels", distinct},
		{"rejected ops leave no trace; DB still usable", emptyTrace && usable},
		{"commit check independent of m=100..10000", scale()},
		{"concurrent snapshots identical and atomic", concurrent()},
		{"SelfCheck", api.New().SelfCheck() == nil},
	}
	for _, c := range checks {
		fmt.Printf("%s: %s\n", c[0], verdict(c[1].(bool)))
	}
	if fails > 0 {
		os.Exit(1)
	}
}
