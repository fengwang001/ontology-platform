package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	_ "unsafe"

	"ontology/alpha"
	"ontology/b32"
	"ontology/codec"
)

var allOK = true

//go:linkname groups ontology/b32.groups
var groups atomic.Int64

func report(name string, cond bool) {
	status := "OK"
	if !cond {
		status, allOK = "FAIL", false
	}
	fmt.Println(status, name)
}

func main() {
	table := true
	for v := byte(0); v < 32; v++ {
		c := alpha.Char(v)
		table = table && alpha.Valid(c) && alpha.Value(c) == v
	}
	report("alpha: 5-bit value <-> char bijection", table)

	shape, padOK := true, true
	pads := map[int]int{1: 6, 2: 4, 3: 3, 4: 1}
	for n := 0; n <= 20; n++ {
		enc := b32.Encode(make([]byte, n))
		shape = shape && len(enc) == 8*((n+4)/5)
		if want, ok := pads[n%5]; ok && n >= 5 {
			padOK = padOK && len(enc)-len(strings.TrimRight(enc, "=")) == want
		}
	}
	report("b32: encoded length 8*ceil(n/5)", shape)
	report("b32: padding table 1..4 -> 6,4,3,1", padOK)

	badIn := []string{"AAAAAAA!", "AAA=AAAA", "AAAAAA==", "AAA====="}
	badErr := []error{b32.ErrInvalidChar, b32.ErrPaddingPosition, b32.ErrPaddingCount, b32.ErrPaddingCount}
	errOK := true
	for i := range badIn {
		b, err := b32.Decode(badIn[i])
		errOK = errOK && errors.Is(err, badErr[i]) && b == nil
	}
	report("b32: three distinct decode errors", errOK)

	victim, before := "AAAAAA==", "AAAAAA=="
	b32.Decode(victim)
	report("b32: rejected input unmodified", victim == before)

	groupsOK := true
	for _, n := range []int{1000, 100000} {
		start := groups.Load()
		b32.Encode(make([]byte, n))
		groupsOK = groupsOK && groups.Load()-start == int64((n+4)/5)
	}
	report("b32: group counts == ceil(n/5)", groupsOK)

	report("codec: SelfCheck roundtrip 0-20", codec.SelfCheck() == nil)

	inputs := [][]byte{{}, {0}, []byte("hello"), make([]byte, 20), make([]byte, 999)}
	enc := make([]string, len(inputs))
	for i, b := range inputs {
		enc[i] = codec.EncodeString(string(b))
	}
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, b := range inputs {
				s := codec.EncodeString(string(b))
				d, err := codec.DecodeString(s)
				if s != enc[i] || err != nil || d != string(b) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	report("codec: concurrent results identical", !bad.Load())

	if !allOK {
		os.Exit(1)
	}
}
