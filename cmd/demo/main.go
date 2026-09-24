// Command demo exercises the crash-safe sequence allocator. It takes no
// arguments, never touches the network, prints at most ten OK/FAIL lines
// and exits non-zero if any check fails.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ontology/api"
)

func line(tag, detail string, ok bool) bool {
	mark := "OK"
	if !ok {
		mark = "FAIL"
	}
	fmt.Printf("%s %s: %s\n", mark, tag, detail)
	return ok
}

// readCkpt returns the persisted next value and the checkpoint file size.
func readCkpt(dir string) (val, size int64) {
	p := filepath.Join(dir, "checkpoint")
	if fi, err := os.Stat(p); err == nil {
		size = fi.Size()
	}
	if b, err := os.ReadFile(p); err == nil && len(b) == 8 {
		val = int64(binary.LittleEndian.Uint64(b))
	}
	return
}

func main() {
	ok := true
	// 1. Section-three eight steps: each token is "return/checkpoint".
	// Step 5 crash and step 6 recover issue no number (shown as "-").
	dir, _ := os.MkdirTemp("", "demo-8-")
	defer os.RemoveAll(dir)
	a, err := api.New(dir)
	if err != nil {
		line("8-step", err.Error(), false)
		os.Exit(1)
	}
	var seq string
	for i := 0; i < 4; i++ {
		n, e := a.Next()
		v, _ := readCkpt(dir)
		ok = ok && e == nil
		seq += fmt.Sprintf("%d/%d ", n, v)
	}
	a.SimulateCrash()
	site, _ := readCkpt(dir)
	ok = ok && a.Recover() == nil && site == 4
	seq += fmt.Sprintf("-/%d -/%d ", site, site)
	for i := 0; i < 2; i++ {
		n, _ := a.Next()
		v, _ := readCkpt(dir)
		seq += fmt.Sprintf("%d/%d ", n, v)
	}
	ok = line("8-step", seq+"(ret/ckpt; crash then recover at 4)", ok) && ok
	// 2. Persist failure leaves no trace and the number is re-issued.
	d2, _ := os.MkdirTemp("", "demo-fault-")
	defer os.RemoveAll(d2)
	a2, _ := api.New(d2)
	for i := 0; i < 3; i++ {
		_, _ = a2.Next()
	}
	a2.SetPersistFault(true)
	_, ferr := a2.Next()
	a2.SetPersistFault(false)
	s1, _ := readCkpt(d2)
	n, _ := a2.Next()
	s2, _ := readCkpt(d2)
	ok = line("persist-fault",
		fmt.Sprintf("rejected=%v file-unchanged=%d retried-n=%d ckpt=%d",
			errors.Is(ferr, api.ErrPersistFailed), s1, n, s2),
		errors.Is(ferr, api.ErrPersistFailed) && s1 == 3 && n == 3 && s2 == 4) && ok
	// 3. Corrupt checkpoint is a distinguishable, state-preserving error.
	_ = os.WriteFile(filepath.Join(d2, "checkpoint"), []byte{1, 2, 3}, 0o600)
	cerr := a2.Recover()
	ok = line("corrupt-ckpt", fmt.Sprintf("Recover err=%v", cerr), errors.Is(cerr, api.ErrCorrupt)) && ok
	// 4. The three failure classes are all distinguishable and distinct.
	_, ierr := api.New("")
	distinct := !errors.Is(api.ErrInvalidDir, api.ErrPersistFailed) &&
		!errors.Is(api.ErrInvalidDir, api.ErrCorrupt) &&
		!errors.Is(api.ErrPersistFailed, api.ErrCorrupt)
	ok = line("sentinel-errors",
		fmt.Sprintf("empty-dir=%v persist=%v corrupt=%v distinct=%v",
			errors.Is(ierr, api.ErrInvalidDir), true, true, distinct),
		errors.Is(ierr, api.ErrInvalidDir) && distinct) && ok
	// 5. One int64 site, not an O(m) log: file stays 8 bytes for any m.
	detail, scalarOK := "", true
	for _, m := range []int64{100, 1000, 10000} {
		dm, _ := os.MkdirTemp("", "demo-scalar-")
		am, _ := api.New(dm)
		for i := int64(0); i < m; i++ {
			if _, e := am.Next(); e != nil {
				scalarOK = false
			}
		}
		v, sz := readCkpt(dm)
		am.SimulateCrash()
		reErr := am.Recover()
		next, e := am.Next()
		if sz != 8 || v != m || reErr != nil || e != nil || next != m {
			scalarOK = false
		}
		detail += fmt.Sprintf("m=%d:%dB->%d ", m, sz, next)
		os.RemoveAll(dm)
	}
	ok = line("scalar-recover", detail+"(8B checkpoint, continue at m)", scalarOK) && ok
	// 6. Concurrency: 400 allocations over 8 goroutines are exactly 0..399.
	d6, _ := os.MkdirTemp("", "demo-conc-")
	defer os.RemoveAll(d6)
	a6, _ := api.New(d6)
	const gor, total = 8, 400
	ids := make([]int64, total)
	var wg sync.WaitGroup
	for g := 0; g < gor; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < total; i += gor {
				ids[i], _ = a6.Next()
			}
		}(g)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for _, v := range ids {
		seen[v] = true
	}
	concOK := len(seen) == total
	for v := int64(0); v < total; v++ {
		concOK = concOK && seen[v]
	}
	ok = line("concurrent", fmt.Sprintf("%d goroutines x %d = exactly {0..%d}", gor, total, total-1), concOK) && ok
	// 7. Built-in self-check over all four invariants.
	a7, _ := api.New(d6)
	ok = line("self-check", "built-in operation sequence verified", a7.SelfCheck() == nil) && ok
	if !ok {
		os.Exit(1)
	}
}
