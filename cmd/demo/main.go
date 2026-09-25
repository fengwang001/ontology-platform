// Command demo exercises the SSI engine end to end and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/kv"
	"ontology/ssi"
)

var failed bool

func fail(format string, a ...any) { failed = true; fmt.Printf("FAIL: "+format+"\n", a...) }

func stateCode(m map[string]string) string {
	code := map[string]byte{"-10": '-', "10": '1', "5": '5', "99": '9'}
	return string([]byte{code[m["x"]], code[m["y"]], code[m["z"]]})
}

func flagsCode(tx *ssi.Txn) string {
	in, out := tx.Flags()
	d := map[bool]byte{false: '0', true: '1'}
	return string([]byte{d[in], d[out]})
}

func seed1(d *api.DB, k, v string) {
	t := d.Begin()
	_ = t.Write(k, v)
	_ = t.Commit()
}

func main() {
	// The 14-step scenario from NOTES.md; token per step: T1T2T3 flags/state.
	mgr := ssi.NewManager(kv.New(map[string]string{"x": "10", "y": "10", "z": "5"}))
	var t1, t2, t3 *ssi.Txn
	var t3read string
	steps := []func() error{
		func() error { t1 = mgr.Begin(); return nil },
		func() error { t2 = mgr.Begin(); return nil },
		func() error { _, e := t1.Read("x"); return e },
		func() error { _, e := t2.Read("x"); return e },
		func() error { _, e := t1.Read("y"); return e },
		func() error { _, e := t2.Read("y"); return e },
		func() error { t3 = mgr.Begin(); return nil },
		func() error { return t3.Write("z", "99") },
		func() error { v, e := t3.Read("z"); t3read = v; return e },
		func() error { return t1.Write("x", "-10") },
		func() error { return t1.Commit() },
		func() error { return t3.Commit() },
		func() error { return t2.Write("y", "-10") },
		func() error { return t2.Commit() },
	}
	want := strings.Fields(`000000/115 000000/115 000000/115 000000/115 000000/115 000000/115 000000/115 000000/115 000000/115 000000/115 000000/-15 000000/-19 000000/-19 001100/-19`)
	tokens := make([]string, 0, 14)
	for i, do := range steps {
		err := do()
		if i == 13 && !errors.Is(err, ssi.ErrConflict) {
			fail("step14 T2.Commit = %v, want ErrConflict", err)
		} else if i < 13 && err != nil {
			fail("step %d: %v", i+1, err)
		}
		tok := flagsCode(t1) + flagsCode(t2) + flagsCode(t3) + "/" + stateCode(mgr.Committed())
		if tok != want[i] {
			fail("step %d token %s, want %s", i+1, tok, want[i])
		}
		tokens = append(tokens, tok)
	}
	fmt.Printf("trace 01-07: %v\n", tokens[:7])
	fmt.Printf("trace 08-14: %v\n", tokens[7:])
	if c := mgr.Committed(); c["x"] != "-10" || c["y"] != "10" || c["z"] != "99" || len(c) != 3 {
		fail("final state %v", c)
	}
	fmt.Println("final: x=-10 y=10 z=99; T2 rolled back (in+out flags set)")
	if t3read != "99" {
		fail("T3.Read(z) = %q", t3read)
	}
	fmt.Println("read-own-write: T3.Read(z)=99")
	db := api.New()
	if err := db.SelfCheck(); err != nil {
		fail("SelfCheck: %v", err)
	}
	fmt.Println("selfcheck: serial-replay, write-skew, read-your-writes, no-trace OK")
	if errors.Is(api.ErrNoActiveTxn, api.ErrEmptyKey) || errors.Is(api.ErrEmptyKey, api.ErrTxnEnded) ||
		errors.Is(api.ErrNoActiveTxn, api.ErrTxnEnded) {
		fail("sentinels not distinct")
	}
	fmt.Println("sentinels: ErrNoActiveTxn / ErrEmptyKey / ErrTxnEnded distinct")
	d := api.New() // commit-check count is index-bounded (see TestComplexityExaminedCount)
	seed1(d, "hot", "0")
	for i := 0; i < 10000; i++ {
		r := d.Begin()
		if _, err := r.Read("hot"); err != nil || r.Commit() != nil {
			fail("reader %d: %v", i, err)
		}
	}
	w := d.Begin()
	_ = w.Write("hot", "1")
	if err := w.Commit(); err != nil || d.Committed()["hot"] != "1" {
		fail("writer over 10000 committed readers: %v", err)
	}
	fmt.Println("commit-checks: writer commits over m=10000 committed readers (index-bounded)")
	checkConcurrent()
	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}

// checkConcurrent: 8 readers begin before a commit lands and must all still
// read the identical pre-commit snapshot.
func checkConcurrent() {
	db := api.New()
	seed1(db, "k", "old")
	const n = 8
	began, release := make(chan struct{}, n), make(chan struct{})
	var wg sync.WaitGroup
	bad := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx := db.Begin()
			began <- struct{}{}
			<-release
			if v, err := tx.Read("k"); err != nil || v != "old" {
				bad <- fmt.Sprintf("%q,%v", v, err)
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-began
	}
	w := db.Begin()
	_ = w.Write("k", "new")
	if err := w.Commit(); err != nil {
		fail("concurrent writer: %v", err)
	}
	close(release)
	wg.Wait()
	close(bad)
	for b := range bad {
		fail("reader saw %s after concurrent commit", b)
	}
	fmt.Println("concurrent: 8 readers saw identical snapshot across a concurrent commit")
}
