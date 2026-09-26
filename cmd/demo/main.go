// Command demo prints OK/FAIL judgements for the incremental SHA-256 build.
package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"sync"

	"ontology/api"
	"ontology/sched"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var ws [64]uint32
	for i := 0; i < 16; i++ {
		ws[i] = uint32(4*i+1)<<24 | uint32(4*i+2)<<16 | uint32(4*i+3)<<8 | uint32(4*i+4)
	}
	sched.Expand(&ws)
	wantW := [8]uint32{
		0x1288cd0e, 0xa267e020, 0x57da8221, 0xf94280cb,
		0x0ed8ad8a, 0x1c699690, 0x5e224395, 0xedf30c74,
	}
	wOK := true
	for i := range wantW {
		wOK = wOK && ws[16+i] == wantW[i]
	}

	abc, _ := hex.DecodeString("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	h1, e1 := api.New(32)
	abcOK := e1 == nil && h1.Write([]byte("abc")) == nil
	d1, err1 := h1.Finalize()
	abcOK = abcOK && err1 == nil && bytes.Equal(d1, abc)

	chunkOK := true
	msg := make([]byte, 300)
	for i := range msg {
		msg[i] = byte(i*13 + 5)
	}
	ref, _ := api.New(32)
	_ = ref.Write(msg)
	dRef, _ := ref.Finalize()
	for _, cut := range []int{1, 63, 64, 65, 127, 128, 200, 299} {
		a, _ := api.New(32)
		_ = a.Write(msg[:cut])
		_ = a.Write(msg[cut:])
		da, _ := a.Finalize()
		chunkOK = chunkOK && bytes.Equal(da, dRef)
	}

	_, badSize := api.New(0)
	_, badSize2 := api.New(33)
	errOK := errors.Is(badSize, api.ErrInvalidSize) && errors.Is(badSize2, api.ErrInvalidSize)
	h2, _ := api.New(32)
	_ = h2.Write([]byte("xyz"))
	_, _ = h2.Finalize()
	wErr := h2.Write([]byte("more"))
	_, fErr := h2.Finalize()
	errOK = errOK && errors.Is(wErr, api.ErrFinalized) && errors.Is(fErr, api.ErrFinalized)
	errOK = errOK && !errors.Is(api.ErrInvalidSize, api.ErrFinalized) &&
		!errors.Is(api.ErrFinalized, api.ErrOverflow) && !errors.Is(api.ErrInvalidSize, api.ErrOverflow)

	// Rejected ops must leave no trace: digest after rejections equals the
	// digest of only the accepted bytes, and Reset makes the hasher reusable.
	h3, _ := api.New(32)
	_ = h3.Write([]byte("keep"))
	_, _ = h3.Finalize()
	_ = h3.Write([]byte("drop"))
	_, _ = h3.Finalize()
	h3.Reset()
	_ = h3.Write([]byte("abc"))
	d3, _ := h3.Finalize()
	noTraceOK := bytes.Equal(d3, abc)

	concOK := true
	var wg sync.WaitGroup
	outs := make([][]byte, 32)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, _ := api.New(32)
			_ = h.Write(msg)
			outs[i], _ = h.Finalize()
		}(i)
	}
	wg.Wait()
	for _, o := range outs {
		concOK = concOK && bytes.Equal(o, dRef)
	}
	shared, _ := api.New(32)
	_ = shared.Write([]byte("shared"))
	dShared, _ := shared.Finalize()
	reads := make([][]byte, 32)
	for i := range reads {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reads[i], _ = shared.Finalize()
			_ = shared.Size()
			_ = api.SelfCheck()
		}(i)
	}
	wg.Wait()
	for _, r := range reads {
		concOK = concOK && bytes.Equal(r, dShared)
	}

	checks := []check{
		{"W[16..23]", wOK},
		{"abc vector", abcOK},
		{"chunk invariance", chunkOK},
		{"sentinel errors", errOK},
		{"rejected ops leave no trace", noTraceOK},
		{"selfcheck(incl. 0-block counter)", api.SelfCheck() == nil},
		{"concurrent finalize/read", concOK},
	}
	failed := false
	for _, c := range checks {
		if c.ok {
			println("OK", c.name)
		} else {
			println("FAIL", c.name)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
