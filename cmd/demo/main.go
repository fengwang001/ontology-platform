package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/mtf"
)

var failed bool

func report(ok bool, name, detail string) {
	status := "OK  "
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

// moveFirstWrong moves to front first, then always emits 0.
func moveFirstWrong(data, alpha []byte) []byte {
	list := append([]byte(nil), alpha...)
	out := make([]byte, len(data))
	for n, s := range data {
		for i, e := range list {
			if e == s {
				list = append([]byte{s}, append(list[:i], list[i+1:]...)...)
				break
			}
		}
		out[n] = 0
	}
	return out
}

func main() {
	alpha := []byte("abcdef")
	data := []byte("ababaadd")
	want := []byte{0, 1, 1, 1, 1, 0, 3, 0}

	// 1. Eight-step indices and output sequence.
	s0, _ := api.New(alpha)
	enc, _ := s0.Encode(data)
	report(string(enc) == string(want), "8-step/output", fmt.Sprint(enc))

	// 2. Move-first-then-output wrong sequence.
	report(string(moveFirstWrong(data, alpha)) == string(make([]byte, 8)),
		"move-first wrong", fmt.Sprint(moveFirstWrong(data, alpha)))

	// 3. 1-based wrong sequence and its wrong reconstruction.
	one := append([]byte(nil), enc...)
	for i := range one {
		one[i]++
	}
	d1, _ := api.New(alpha)
	wrong, _ := d1.Decode(one)
	report(string(one) == string([]byte{1, 2, 2, 2, 2, 1, 4, 1}) && string(wrong) == "bcabcbeb",
		"1-based wrong+decode", fmt.Sprintf("%v -> %q", one, wrong))

	// 4. Empty data / single-symbol alphabet / invalid alphabet.
	e, _ := api.New(alpha)
	emptyEnc, emptyErr := e.Encode(nil)
	s, _ := api.New([]byte{'a'})
	singleEnc, singleErr := s.Encode([]byte("aaa"))
	_, badAlpha := api.New([]byte{'a', 'a'})
	report(emptyErr == nil && len(emptyEnc) == 0 && singleErr == nil &&
		string(singleEnc) == "\x00\x00\x00" && errors.Is(badAlpha, api.ErrInvalidAlphabet),
		"empty/single/illegal", fmt.Sprintf("empty=%v single=%v", emptyEnc, singleEnc))

	// 5. Round trip.
	rt, _ := api.New(alpha)
	dec, _ := rt.Decode(enc)
	report(string(dec) == string(data), "round trip", fmt.Sprintf("%q", dec))

	// 6. List invariant (permutation + pos map in sync).
	report(mtf.SelfCheck() == nil, "list invariant", "")

	// 7. Three distinguishable sentinel errors.
	_, uerr := rt.Encode([]byte{'z'})
	_, ierr := rt.Decode([]byte{byte(len(alpha))})
	distinct := errors.Is(uerr, api.ErrUnknownSymbol) && errors.Is(ierr, api.ErrInvalidIndex) &&
		errors.Is(badAlpha, api.ErrInvalidAlphabet) && !errors.Is(uerr, api.ErrInvalidIndex)
	report(distinct, "3 sentinel errors", "")

	// 8. State unchanged and reusable after rejection.
	bad, _ := api.New(alpha)
	bad.Encode([]byte("abz"))
	bad.Decode([]byte{0, 9})
	fresh, _ := api.New(alpha)
	after, _ := bad.Encode([]byte("ab"))
	expect, _ := fresh.Encode([]byte("ab"))
	report(string(after) == string(expect), "state unchanged after reject", fmt.Sprint(after))

	// 9. O(1) location cost independent of alphabet size m.
	report(mtf.CheckConstantTimeLocation() == nil, "O(1) locate over m", "")

	// 10. Concurrent independent instances give byte-identical results.
	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	concOK := true
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			m, err := api.New(alpha)
			if err != nil {
				mu.Lock()
				concOK = false
				mu.Unlock()
				return
			}
			ce, e1 := m.Encode(data)
			cd, e2 := m.Decode(ce)
			if e1 != nil || e2 != nil ||
				string(ce) != string(enc) || string(cd) != string(data) {
				mu.Lock()
				concOK = false
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	report(concOK, "concurrent identical", fmt.Sprintf("goroutines=%d", n))

	if failed {
		os.Exit(1)
	}
}
