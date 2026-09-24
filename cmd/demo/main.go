// Command demo 演示前缀压缩有序字典的各项判定。
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"

	"ontology/block"
	"ontology/dict"
	"ontology/lookup"
	"ontology/scan"
	"ontology/verify"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func blockChecks() {
	entries := make([]string, 1000)
	for i := range entries {
		entries[i] = fmt.Sprintf("key-%06d", i)
	}
	data, _ := block.Encode(entries, 16)
	seen := map[string]bool{}
	sorted := true
	for cut := 1; cut < len(data); cut++ {
		got, err := block.Decode(data[:cut])
		for name, e := range map[string]error{
			"header": block.ErrHeaderIncomplete, "table": block.ErrRestartTable,
			"entry": block.ErrEntryIncomplete, "crc": block.ErrCRC,
		} {
			if errors.Is(err, e) {
				seen[name] = true
			}
		}
		sorted = sorted && slices.IsSorted(got)
	}
	check("truncate-4-class", len(seen) == 4, fmt.Sprintf("classes=%d/4 cuts=1..%d", len(seen), len(data)-1))
	check("recover-sorted", sorted, "recovered prefix strictly sorted at every cut")

	lie, _ := block.Encode([]string{"apple", "apricot", "banana"}, 4)
	pos := 16 + 4
	_, n := binary.Uvarint(lie[pos:])
	pos += n
	slen, n := binary.Uvarint(lie[pos:])
	lie[pos+n+int(slen)] = 0x7f
	_, err := block.Decode(lie)
	check("prefix-lie", errors.Is(err, block.ErrPrefixLen), fmt.Sprintf("err=%v", err))

	small := make([]string, 32)
	for i := range small {
		small[i] = fmt.Sprintf("e%03d", i)
	}
	bad, _ := block.Encode(small, 4)
	binary.LittleEndian.PutUint32(bad[20:], binary.LittleEndian.Uint32(bad[20:])+1)
	s, _, err := block.DecodeEntry(bad, 5)
	check("restart-fallback", s == "e005" && errors.Is(err, block.ErrRestartOffset),
		fmt.Sprintf("got=%q err=%v", s, err))
}

func dictChecks() {
	entries := make([]string, 10000)
	for i := range entries {
		entries[i] = fmt.Sprintf("user:%08d", i)
	}
	sizes := make([]int, 0, 4)
	for _, k := range []int{1, 4, 16, 64} {
		d, err := dict.Build(entries, 128, k)
		if err != nil {
			check("dict-build", false, err.Error())
			return
		}
		sizes = append(sizes, len(d.Serialize()))
	}
	check("ratio-K", sizes[0] > sizes[3] && sizes[1] > sizes[2],
		fmt.Sprintf("bytes K=1:%d K=4:%d K=16:%d K=64:%d", sizes[0], sizes[1], sizes[2], sizes[3]))

	d, _ := dict.Build(entries, 128, 16)
	d2, err := dict.Open(d.Serialize())
	s, _, _ := d2.Entry(5432)
	check("dict-roundtrip", err == nil && s == entries[5432] && d2.Count == 10000,
		fmt.Sprintf("entry[5432]=%q blocks=%d", s, d2.NumBlocks()))
}

func main() {
	blockChecks()
	dictChecks()
	scanChecks()
	lookupChecks()
	fmt.Printf("SUMMARY %d/%d passed\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func scanChecks() {
	entries := make([]string, 5000)
	for i := range entries {
		entries[i] = fmt.Sprintf("%05d", i)
	}
	d, _ := dict.Build(entries, 50, 16) // 100 块
	dict.ResetBlockDecodes()
	got, err := scan.Prefix(d, "049")
	check("scan-2-blocks", err == nil && len(got) == 100 && dict.BlockDecodes() == 2,
		fmt.Sprintf("hits=%d blocks-decoded=%d/100", len(got), dict.BlockDecodes()))

	check("verify-invariants", verify.Dict(d) == nil, "blocks sorted, dir matches, counts ok")
	raw := d.Serialize()
	raw[len(raw)-1] ^= 0xff // 破坏最后一个块的 CRC
	dBad, _ := dict.Open(raw)
	check("verify-detects", verify.Dict(dBad) != nil, "corrupted block crc detected")
}

func lookupChecks() {
	entries := make([]string, 100000)
	for i := range entries {
		entries[i] = fmt.Sprintf("key-%07d", i)
	}
	d, _ := dict.Build(entries, 4096, 16)
	posOK := true
	for _, tc := range []struct {
		target string
		idx    int
		found  bool
	}{
		{"key-0004096", 4096, true},   // 恰好是重启点
		{"key-0004101", 4101, true},   // 两个重启点之间
		{"key-0004101~", 4102, false}, // 之间且不存在
		{"aaa", 0, false},             // 小于块首
		{"zzz", 100000, false},        // 大于块尾
	} {
		idx, found := lookup.Find(d, tc.target)
		posOK = posOK && idx == tc.idx && found == tc.found
	}
	check("find-4-pos", posOK, "restart/between/below-head/above-tail")

	rng := rand.New(rand.NewPCG(1, 2))
	maxDecode, maxCmp := int64(0), int64(0)
	for range 500 {
		lookup.ResetCounters()
		lookup.Get(d, rng.IntN(len(entries)))
		dn, _ := lookup.Counters()
		maxDecode = max(maxDecode, dn)
		lookup.ResetCounters()
		lookup.Find(d, entries[rng.IntN(len(entries))])
		_, cn := lookup.Counters()
		maxCmp = max(maxCmp, cn)
	}
	check("get-decodes<=K", maxDecode <= 16, fmt.Sprintf("max=%d K=16", maxDecode))
	bound := int64(4*8 + 16) // 4*ceil(log2(256)) + K
	check("find-compares", maxCmp <= bound, fmt.Sprintf("max=%d bound=%d", maxCmp, bound))
}
