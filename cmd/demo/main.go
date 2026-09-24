package main

import (
	"fmt"
	"math/rand"

	"ontology/chunk"
	"ontology/sig"
)

type check struct {
	name string
	ok   bool
}

func (c check) line() string {
	if c.ok {
		return "OK " + c.name
	}
	return "FAIL " + c.name
}

func main() {
	var checks []check

	// --- chunk 包判定 ---
	data := make([]byte, 197)
	rand.New(rand.NewSource(3)).Read(data)
	sc, _ := chunk.NewScanner(data, 4)
	rollEq := true
	for off := 0; off+4 <= len(data); off++ {
		if sc.Weak() != chunk.Weak(data[off:off+4]) {
			rollEq = false
		}
		sc.Advance()
	}
	checks = append(checks, check{"滚动与重算在每个偏移相等", rollEq})

	x := []byte{221, 251, 75, 153}
	y := []byte{229, 214, 125, 132}
	collisionBlocked := chunk.Weak(x) == chunk.Weak(y) && chunk.Strong(x) != chunk.Strong(y)
	checks = append(checks, check{"弱碰撞被强校验挡住", collisionBlocked})

	// --- sig 包判定 ---
	signature, _ := sig.Generate(data, 4)
	sigDecoded, err := sig.Decode(signature.Encode())
	sigRoundTrip := err == nil && len(sigDecoded.Entries) == len(signature.Entries)
	checks = append(checks, check{"签名编解码往返一致", sigRoundTrip})

	pass := 0
	for _, c := range checks {
		fmt.Println(c.line())
		if c.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL: %d/%d checks passed\n", pass, len(checks))
}
