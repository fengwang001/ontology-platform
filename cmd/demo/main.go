package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/chunk"
	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
	"ontology/verify"
)

type check struct {
	name string
	ok   bool
}

func chunkChecks() []check {
	var out []check

	a := []byte{1, 2, 3, 4}
	b := []byte{0, 2, 3, 5}
	collision := chunk.Weak(a) == chunk.Weak(b) &&
		!bytes.Equal(chunk.Strong(a), chunk.Strong(b))
	out = append(out, check{"weak collision blocked by strong checksum", collision})

	data := make([]byte, 500)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	r, _ := chunk.NewRoller(data, 16)
	rollOK := true
	for r.Valid() {
		if r.Weak() != chunk.Weak(data[r.Pos():r.Pos()+16]) {
			rollOK = false
			break
		}
		if !r.Advance() {
			break
		}
	}
	out = append(out, check{"rolling weak equals recompute at every offset", rollOK})
	out = append(out, check{"weak ops <= 4*data length", r.WeakOps() <= 4*len(data)})
	return out
}

func diffChecks() []check {
	const size = 8
	mk := func(p string, n int) []byte {
		out := make([]byte, 0, n*size)
		for b := 0; b < n; b++ {
			for j := 0; j < size; j++ {
				out = append(out, p[b%len(p)]+byte(j))
			}
		}
		return out
	}
	base := mk("ABCDEFGH", 8)

	same, _ := sig.Generate(base, size)
	pSame, stSame := diff.Build(base, same)

	mid := append([]byte(nil), base...)
	for j := 0; j < size; j++ {
		mid[3*size+j] = byte(0xFF - j)
	}
	pMid, stMid := diff.Build(mid, same)

	return []check{
		{"middle-block diff: literal <= 2*blockSize", pMid.LiteralBytes() <= 2*size},
		{"identical data: literal bytes == 0", pSame.LiteralBytes() == 0},
		{"strong computes <= weak hits", stSame.StrongCount <= stSame.WeakHits &&
			stMid.StrongCount <= stMid.WeakHits},
	}
}

func failureChecks() []check {
	const size = 8
	tgt := make([]byte, 6*size)
	for i := range tgt {
		tgt[i] = byte(i*3 + 1)
	}
	src := append([]byte(nil), tgt...)
	for j := 0; j < size; j++ {
		src[2*size+j] = byte(0xF0 + j)
	}
	s, _ := sig.Generate(tgt, size)
	p, _ := diff.Build(src, s)
	pb := verify.Encode(p, len(tgt))

	// 从编码长度反取四个截断点：头中、指令中、数据中、CRC 损坏。
	p2, _ := verify.Decode(pb)
	instLen := 0
	for _, in := range p2.Instrs {
		instLen += 5
		if in.Op == diff.OpLit {
			break
		}
	}
	dataLen := p.LiteralBytes()
	H := 52
	points := []struct {
		name string
		buf  []byte
		want error
	}{
		{"truncation: header incomplete", pb[:H/2], verify.ErrHeaderTruncated},
		{"truncation: instructions incomplete", pb[:H+instLen/2], verify.ErrInstrTruncated},
		{"truncation: data incomplete", pb[:H+instLen+dataLen/2], verify.ErrDataTruncated},
	}
	crcBad := bytes.Clone(pb)
	crcBad[H+instLen] ^= 0xFF
	points = append(points, struct {
		name string
		buf  []byte
		want error
	}{"truncation: crc mismatch", crcBad, verify.ErrCRCMismatch})

	dir, _ := os.MkdirTemp("", "demo-*")
	defer os.RemoveAll(dir)
	var out []check
	unchangedAll := true
	for _, pt := range points {
		path := filepath.Join(dir, "t.bin")
		os.WriteFile(path, tgt, 0o644)
		err := patch.ApplyFile(pt.buf, path)
		ok := errors.Is(err, pt.want)
		out = append(out, check{pt.name, ok})
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, tgt) {
			unchangedAll = false
		}
	}
	out = append(out, check{"truncated patch never partially applied", unchangedAll})

	// 指令越界。
	pBad, _ := diff.Build(src, s)
	for i := range pBad.Instrs {
		if pBad.Instrs[i].Op == diff.OpRef {
			pBad.Instrs[i].Block = 9999
		}
	}
	_, errRange := patch.ApplyBytes(pBad, tgt)
	out = append(out, check{"out-of-range block ref detected", errors.Is(errRange, verify.ErrBlockOutOfRange)})

	// 签名与目标数据不符：目标在生成签名后被改动。
	modified := bytes.Clone(tgt)
	modified[0] ^= 0xFF
	_, errHash := patch.ApplyBytes(p, modified)
	out = append(out, check{"signature/data drift detected", errors.Is(errHash, verify.ErrResultMismatch)})

	return out
}

func main() {
	checks := append(append(chunkChecks(), diffChecks()...), failureChecks()...)
	pass := 0
	for _, c := range checks {
		if c.ok {
			fmt.Printf("OK   %s\n", c.name)
			pass++
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("checks failed")
	}
}
