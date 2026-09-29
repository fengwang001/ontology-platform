package main

import (
	"fmt"

	"ontology/addr"
	"ontology/roll"
	"ontology/split"
	"ontology/store"
)

type check struct {
	name string
	fn   func() error
}

func main() {
	checks := []check{
		{"roll: O(1) incremental window hash", func() error {
			h, err := roll.New(4)
			if err != nil {
				return err
			}
			data := []byte("abcdefgh")
			for _, b := range data {
				h.Push(b)
			}
			var naive uint64
			for _, b := range data[4:] {
				naive = naive*1099511628211 + uint64(b)
			}
			if h.Sum() != naive || !h.Full() {
				return fmt.Errorf("sum %d want %d", h.Sum(), naive)
			}
			return nil
		}},
		{"addr: same content -> same address, different -> not", func() error {
			a1, a2 := addr.Of([]byte("hello")), addr.Of(append([]byte("hell"), 'o'))
			a3 := addr.Of([]byte("hellp"))
			if !a1.Equal(a2) || a1.Equal(a3) {
				return fmt.Errorf("address collision/divergence")
			}
			return nil
		}},
		{"split: same blocks for any Write segmentation; min/max lengths", func() error {
			data := lcgBytes(5000, 1)
			cfg := split.Config{Min: 64, Max: 128, Window: 8, Bits: 5}
			cuts := [][]int{{len(data)}, ones(len(data)), {37, 1000, 3000, len(data) - 4037}}
			var ref []split.Chunk
			for ci, c := range cuts {
				got := feed(cfg, data, c)
				if ci == 0 {
					ref = got
					continue
				}
				if !sameChunks(ref, got) {
					return fmt.Errorf("segmentation %d differs", ci)
				}
			}
			for i, ch := range ref {
				n := int(ch.End - ch.Start)
				if i < len(ref)-1 && (n < cfg.Min || n > cfg.Max) {
					return fmt.Errorf("chunk %d len %d out of range", i, n)
				}
			}
			return nil
		}},
		{"split: max forces a cut; tail shorter than min stays its own block", func() error {
			cfg := split.Config{Min: 16, Max: 32, Window: 4, Bits: 13}
			data := forceMaxData(cfg, 2*cfg.Max+5) // 两个满块 + 5 字节短尾
			got, err := split.Segment(cfg, data)
			if err != nil {
				return err
			}
			if len(got) != 3 || int(got[0].End-got[0].Start) != cfg.Max ||
				int(got[1].End-got[1].Start) != cfg.Max ||
				int(got[2].End-got[2].Start) != 5 {
				return fmt.Errorf("got %v", lens(got))
			}
			return nil
		}},
		{"split: stream shorter than min is one block; empty is no blocks", func() error {
			cfg := split.Config{Min: 64, Max: 128, Window: 8, Bits: 5}
			short, err := split.Segment(cfg, []byte("tiny"))
			if err != nil || len(short) != 1 || int(short[0].End) != 4 {
				return fmt.Errorf("short: %v %v", short, err)
			}
			none, err := split.Segment(cfg, nil)
			if err != nil || len(none) != 0 {
				return fmt.Errorf("empty: %v %v", none, err)
			}
			return nil
		}},
		{"split: distinct errors for min>max and window>min", func() error {
			if _, err := split.New(split.Config{Min: 10, Max: 9, Window: 4}); err != split.ErrBadRange {
				return fmt.Errorf("range err = %v", err)
			}
			if _, err := split.New(split.Config{Min: 4, Max: 10, Window: 5}); err != split.ErrBadWindow {
				return fmt.Errorf("window err = %v", err)
			}
			return nil
		}},
		{"store: identical blocks share one address and refcount; missing address distinct error", func() error {
			st := store.New(store.Limits{})
			a, err := st.Put([]byte("payload"))
			if err != nil {
				return err
			}
			b, _ := st.Put([]byte("payload"))
			if !a.Equal(b) || st.Len() != 1 || st.Refs(a) != 2 || st.Bytes() != 7 {
				return fmt.Errorf("dedup state wrong")
			}
			got, err := st.Get(a)
			if err != nil || string(got) != "payload" {
				return fmt.Errorf("get failed")
			}
			if _, err := st.Get(addr.Of([]byte("nope"))); err != store.ErrNotFound {
				return fmt.Errorf("want ErrNotFound, got %v", err)
			}
			return nil
		}},
		{"store: capacity rejection is atomic and leaves no trace", func() error {
			st := store.New(store.Limits{MaxBlocks: 1, MaxBytes: 10})
			if _, err := st.Put([]byte("aaaa")); err != nil {
				return err
			}
			if _, err := st.Put([]byte("bbbbb")); err != store.ErrTooManyBlocks {
				return fmt.Errorf("block limit err = %v", err)
			}
			st2 := store.New(store.Limits{MaxBytes: 10})
			if _, err := st2.Put([]byte("this-is-too-long")); err != store.ErrTooManyBytes {
				return fmt.Errorf("byte limit err = %v", err)
			}
			if st.Len() != 1 || st.Bytes() != 4 || st2.Len() != 0 || st2.Bytes() != 0 {
				return fmt.Errorf("state changed after rejection: %d blocks %d bytes", st.Len(), st.Bytes())
			}
			return nil
		}},
	}
	fail := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			fail++
			continue
		}
		fmt.Printf("OK   %s\n", c.name)
	}
	if fail != 0 {
		fmt.Printf("%d/%d checks failed\n", fail, len(checks))
		return
	}
}

func lcgBytes(n int, seed uint64) []byte {
	out := make([]byte, n)
	x := seed
	for i := range out {
		x = x*6364136223846793005 + 1442695040888963407
		out[i] = byte(x >> 33)
	}
	return out
}

func feed(cfg split.Config, data []byte, cut []int) []split.Chunk {
	c, _ := split.New(cfg)
	var all []split.Chunk
	pos := 0
	for _, n := range cut {
		end := pos + n
		if end > len(data) {
			end = len(data)
		}
		ch, _ := c.Write(data[pos:end])
		all = append(all, ch...)
		pos = end
	}
	tail, _ := c.Flush()
	return append(all, tail...)
}

func sameChunks(a, b []split.Chunk) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Start != b[i].Start || a[i].End != b[i].End ||
			string(a[i].Data) != string(b[i].Data) {
			return false
		}
	}
	return true
}

func lens(cs []split.Chunk) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = int(c.End - c.Start)
	}
	return out
}

// forceMaxData 贪心地构造在 cfg 下只在 max 处切块的数据。
func forceMaxData(cfg split.Config, n int) []byte {
	data := make([]byte, 0, n)
	for len(data) < n {
		found := false
		for b := 0; b < 256 && !found; b++ {
			trial := append(append([]byte{}, data...), byte(b))
			if !hasContentCut(cfg, trial) {
				data = trial
				found = true
			}
		}
		if !found {
			data = append(data, 0)
		}
	}
	return data
}

func ones(n int) []int {
	c := make([]int, n)
	for i := range c {
		c[i] = 1
	}
	return c
}

// hasContentCut 报告 data 在 cfg 下（强制切只重置 min 区）是否出现内容边界。
func hasContentCut(cfg split.Config, data []byte) bool {
	h, _ := roll.New(cfg.Window)
	mask := roll.Mask(cfg.Bits)
	chunkLen := 0
	for _, b := range data {
		h.Push(b)
		chunkLen++
		if chunkLen >= cfg.Max {
			chunkLen = 0
			continue
		}
		if chunkLen >= cfg.Min && h.Full() && h.Sum()&mask == 0 {
			return true
		}
	}
	return false
}
