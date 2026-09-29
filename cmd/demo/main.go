package main

import (
	"fmt"

	"ontology/addr"
	"ontology/store"
	"errors"
	"ontology/roll"
	"ontology/split"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	h, err := roll.New(8)
	ok := err == nil
	for _, b := range []byte("abcdefgh") {
		h.Push(b)
	}
	s1 := h.Sum()
	h.Reset()
	for _, b := range []byte("abcdefgh") {
		h.Push(b)
	}
	report("roll", ok && h.Full() && h.Sum() == s1)

	cfg := split.Config{Min: 256, Max: 4096, Window: 16}
	data := make([]byte, 120000)
	x := uint32(747796405)
	for i := range data {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		data[i] = byte(x >> 8)
	}
	bs, serr := split.Boundaries(data, cfg)
	sok := serr == nil
	prev, forced := 0, false
	for _, b := range bs {
		sok = sok && b-prev >= cfg.Min && b-prev <= cfg.Max
		if b-prev == cfg.Max {
			forced = true
		}
		prev = b
	}
	report("split lengths/max-force", sok && forced)
	report("addr stable+distinct",
		addr.Of([]byte("blob")).Equal(addr.Of([]byte("blob"))) &&
			!addr.Of([]byte("blob")).Equal(addr.Of([]byte("bloc"))))

	st := store.New(store.Limits{MaxBlocks: 2, MaxBytes: 100})
	sa, _ := st.Put([]byte("dup"))
	st.Put([]byte("dup"))
	st.Put([]byte("other"))
	_, e1 := st.Put([]byte("overflow-block"))
	_, e2 := st.Get(addr.Of([]byte("nope")))
	report("store dedup/refs/caps",
		st.BlockCount() == 2 && st.Refs(sa) == 2 &&
			errors.Is(e1, store.ErrTooManyBlocks) &&
			errors.Is(e2, store.ErrNotFound) &&
			st.BlockCount() == 2) // 拒绝后无残留
}
