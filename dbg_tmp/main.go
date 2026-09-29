package main

import (
	"fmt"
	"ontology/split"
)

func main() {
	cfg := split.Config{Min: 64, Max: 128, Window: 8, Bits: 5}
	data := lcg(5000)
	a, _ := split.Segment(cfg, data)
	c, _ := split.New(cfg)
	var b []split.Chunk
	for _, x := range data {
		ch, _ := c.Write([]byte{x})
		b = append(b, ch...)
	}
	t, _ := c.Flush()
	b = append(b, t...)
	fmt.Println(len(a), len(b))
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int = -1, -1
		if i < len(a) { x = int(a[i].End) }
		if i < len(b) { y = int(b[i].End) }
		if x != y { fmt.Println("first diff at", i, "allEnd", x, "byteEnd", y); break }
	}
}
func lcg(n int) []byte {
	out := make([]byte, n)
	var x uint64 = 1
	for i := range out {
		x = x*6364136223846793005 + 1442695040888963407
		out[i] = byte(x >> 33)
	}
	return out
}
