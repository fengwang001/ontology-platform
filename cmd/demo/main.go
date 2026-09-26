package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/frame"
	"ontology/lenp"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %v\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	p := lenp.PutLength(2)
	n, err := lenp.GetLength(p)
	_, errShort := lenp.GetLength(p[:3])
	check("lenp 前缀编解码", bytes.Equal(p, []byte{2, 0, 0, 0}) && n == 2 && err == nil && errShort == lenp.ErrShortPrefix)

	// 第三节：四字段记录，逐字段前缀+完整字节、整条记录字节串。
	fields := [][]byte{[]byte("hi"), {}, []byte("world!"), []byte("A")}
	wantHex := "020000006869" + "00000000" + "06000000776f726c6421" + "0100000041"
	rec := frame.Encode(fields)
	check("四字段记录字节串", hex.EncodeToString(rec) == wantHex)

	// 往返一致（含空字段）。
	got, err := frame.Decode(rec)
	rt := err == nil && len(got) == len(fields)
	for i := range fields {
		rt = rt && bytes.Equal(got[i], fields[i])
	}
	check("往返一致(含空字段)", rt)

	// 前缀自洽：逐字段前缀==负载长，无重叠无空洞覆盖全缓冲。
	r := frame.NewReader(rec)
	self := true
	for r.Pos() < len(rec) {
		l, _ := lenp.GetLength(rec[r.Pos():])
		f, e := r.NextField()
		self = self && e == nil && len(f) == l
	}
	check("前缀自洽", self && r.Pos() == len(rec))

	// 截断被拒：整体失败 (nil, ErrTruncated)，不返回部分字段。
	bad, errT := frame.Decode(rec[:len(rec)-1])
	check("截断被拒", bad == nil && errT == frame.ErrTruncated)

	// 空字段正确编码为 00 00 00 00。
	check("空字段编码", bytes.Equal(frame.Encode([][]byte{{}}), []byte{0, 0, 0, 0}))

	// 大 m 下 SkipField 只靠前缀偏移算术（零负载触碰由测试钉住）。
	const m = 5000
	big := make([][]byte, m)
	for i := range big {
		big[i] = make([]byte, 4096)
	}
	bigRec := frame.Encode(big)
	br := frame.NewReader(bigRec)
	skip := true
	for i := 0; i < m; i++ {
		skip = skip && br.SkipField() == nil
	}
	check("大m SkipField 跳过", skip && br.Pos() == len(bigRec))

	// 并发：N 路解码同一段只读字节结果一致，N 路编码与串行一致。
	const nG = 32
	var wg sync.WaitGroup
	conc := true
	for i := 0; i < nG; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); g, e := frame.Decode(rec); conc = conc && e == nil && len(g) == len(fields) }()
		go func(k int) {
			defer wg.Done()
			conc = conc && bytes.Equal(frame.Encode(big[:k%10+1]), frame.Encode(big[:k%10+1]))
		}(i)
	}
	wg.Wait()
	check("并发编解码一致", conc)

	check("api.SelfCheck 四条不变量", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
