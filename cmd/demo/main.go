// Command demo exercises the sched/sha/api packages and prints OK/FAIL lines.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/sched"
	"ontology/sha"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	check("schedule W[16..23]", checkSchedule())
	check("abc vector", checkVector())
	check("split invariance", checkSplits())
	check("three distinct errors", checkErrors())
	check("rejection leaves no trace", checkNoTrace())
	check("1-byte write compresses 0 blocks", checkCompressCount())
	check("concurrent finalize consistent", checkConcurrent())
	check("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

// checkSchedule expands the 0x01..0x40 block; W[16..23] must match NOTES.md.
func checkSchedule() bool {
	block := make([]byte, sched.BlockSize)
	for i := range block {
		block[i] = byte(i + 1)
	}
	var w [64]uint32
	sched.Expand(block, &w)
	want := [8]uint32{0x1288cd0e, 0xa267e020, 0x57da8221, 0xf94280cb, 0x0ed8ad8a, 0x1c699690, 0x5e224395, 0xedf30c74}
	s, ok := "", true
	for t := 16; t < 24; t++ {
		s += fmt.Sprintf("%08x ", w[t])
		ok = ok && w[t] == want[t-16]
	}
	fmt.Println("W[16..23]:", s)
	return ok
}

func checkVector() bool {
	h, _ := api.New(32)
	if h.Write([]byte("abc")) != nil {
		return false
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	return fmt.Sprintf("%x", h.Finalize()) == want
}

func checkSplits() bool {
	msg := make([]byte, 1000)
	for i := range msg {
		msg[i] = byte(i*31 + 5)
	}
	one, _ := api.New(32)
	if one.Write(msg) != nil {
		return false
	}
	ref := one.Finalize()
	for cut := 0; cut <= len(msg); cut += 7 {
		two, _ := api.New(32)
		if two.Write(msg[:cut]) != nil || two.Write(msg[cut:]) != nil || !bytes.Equal(two.Finalize(), ref) {
			return false
		}
	}
	return true
}

func checkErrors() bool {
	_, errSize := api.New(0)
	h, _ := api.New(32)
	_ = h.Write([]byte("abc"))
	h.Finalize()
	errFin := h.Write([]byte("x"))
	errLen := sha.NewWithLimit(64).Write(make([]byte, 65))
	return errors.Is(errSize, api.ErrSize) && errors.Is(errFin, sha.ErrFinalized) && errors.Is(errLen, sha.ErrLength) &&
		errSize != errFin && errFin != errLen && errSize != errLen
}

func checkNoTrace() bool {
	h, _ := api.New(32)
	_ = h.Write([]byte("abc"))
	before := h.Finalize()
	if h.Write([]byte("junk")) == nil || !bytes.Equal(h.Finalize(), before) {
		return false
	}
	h.Reset()
	_ = h.Write([]byte("abc"))
	return bytes.Equal(h.Finalize(), before)
}

// checkCompressCount reads the unexported blocks counter via reflection
// (no exported accessor allowed): a 1-byte Write after m full blocks
// must compress 0 blocks.
func checkCompressCount() bool {
	for _, m := range []int{100, 1000, 10000} {
		sh := sha.New()
		if sh.Write(make([]byte, m*sched.BlockSize)) != nil || sh.Write([]byte{0}) != nil {
			return false
		}
		if reflect.ValueOf(sh).Elem().FieldByName("blocks").Int() != 0 {
			return false
		}
	}
	return true
}

func checkConcurrent() bool {
	msg := []byte("concurrent sha-256 input")
	mk := func() *api.Hasher { h, _ := api.New(32); _ = h.Write(msg); return h }
	want, shared := mk().Finalize(), mk()
	shared.Finalize()
	const n = 64
	start, bad := make(chan struct{}), make(chan bool, 2*n)
	var wg sync.WaitGroup
	run := func(f func() bool) {
		defer wg.Done()
		<-start
		if !f() {
			bad <- true
		}
	}
	for i := 0; i < n; i++ {
		wg.Add(2)
		go run(func() bool { return bytes.Equal(mk().Finalize(), want) })
		go run(func() bool { return bytes.Equal(shared.Finalize(), want) && shared.Size() == 32 })
	}
	close(start)
	wg.Wait()
	close(bad)
	return len(bad) == 0
}
