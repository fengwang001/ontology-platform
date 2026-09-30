// demo 逐项演练 LZ77 压缩器的语义承诺，全部通过时退出码为 0。
package main

import "bytes"
import "errors"
import "fmt"
import "math/rand"
import "os"
import "ontology/dec"
import "ontology/enc"
import "ontology/wire"

var ec = enc.Config{Window: 1 << 20, MaxChain: 128}
var dc = dec.Config{Window: 1 << 20, MaxOutput: 1 << 30}

func compress(data []byte, chunk int) []byte {
	var buf bytes.Buffer
	e, _ := enc.New(&buf, ec)
	for off := 0; off < len(data); off += chunk {
		e.Write(data[off:min(off+chunk, len(data))])
	}
	e.Close()
	return buf.Bytes()
}

func decompress(stream []byte, chunk int) ([]byte, error) {
	d, _ := dec.New(dc)
	for off := 0; off < len(stream); off += chunk {
		if _, err := d.Write(stream[off:min(off+chunk, len(stream))]); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}

func main() {
	fails := 0
	ok := func(name string, cond bool) {
		if !cond {
			fails++
		}
		fmt.Printf("%-20s %v\n", name, map[bool]string{true: "OK", false: "FAIL"}[cond])
	}
	rnd := rand.New(rand.NewSource(1))
	r := make([]byte, 40000)
	rnd.Read(r)
	kinds := map[string][]byte{"空": {}, "全同": bytes.Repeat([]byte{9}, 50000), "随机": r,
		"周期": bytes.Repeat([]byte("abc"), 10000), "混合": append(bytes.Repeat([]byte("hello world "), 2000), r...)}
	good := true
	for _, d := range kinds {
		out, err := decompress(compress(d, 4096), 1024)
		good = good && err == nil && bytes.Equal(out, d)
	}
	ok("五类输入往返", good)
	good = true
	for _, dist := range []int{1, 2, 3} {
		want := []byte("abc")[:dist]
		for len(want) < dist+500 {
			want = append(want, want[len(want)-dist])
		}
		s := wire.AppendBackref(wire.AppendLiteral(wire.Header(), want[:dist]), dist, 500)
		s = wire.AppendEnd(s, uint64(len(want)), wire.Checksum(want))
		out, err := decompress(s, 3)
		good = good && err == nil && bytes.Equal(out, want)
	}
	ok("重叠回指 dist=1/2/3", good)
	data := kinds["混合"]
	ok("三种Write切法相同", bytes.Equal(compress(data, 1), compress(data, 7)) &&
		bytes.Equal(compress(data, 7), compress(data, len(data))))
	var buf bytes.Buffer
	e, _ := enc.New(&buf, ec)
	e.Write(data[:5000])
	e.Flush()
	d0, _ := dec.New(dc)
	d0.Write(buf.Bytes())
	n := buf.Len()
	e.Flush()
	ok("Flush还原全部已写", bytes.Equal(d0.Output(), data[:5000]))
	ok("连续Flush零字节", buf.Len() == n)
	s := compress(data, 4096)
	o1, _ := decompress(s, 1)
	o2, _ := decompress(s, len(s))
	ok("解压切法无关", bytes.Equal(o1, data) && bytes.Equal(o2, data))
	empty := compress(nil, 1)
	o3, err := decompress(empty, 1)
	dZ, _ := dec.New(dc)
	ok("空流合法/零长截断", len(empty) > 0 && err == nil && len(o3) == 0 && errors.Is(dZ.Close(), wire.ErrTruncated))
	good = true
	for _, c := range []struct {
		s    []byte
		want error
	}{
		{append([]byte("XLZ\x01"), s[4:]...), wire.ErrBadMagic},
		{append([]byte("OLZ\x02"), s[4:]...), wire.ErrBadVersion},
		{wire.AppendBackref(wire.Header(), 0, 5), wire.ErrZeroDistance},
		{wire.AppendBackref(wire.AppendLiteral(wire.Header(), []byte("ab")), 3, 1), wire.ErrDistanceTooFar},
		{wire.AppendBackref(wire.AppendLiteral(wire.Header(), []byte("ab")), 1<<21, 1), wire.ErrDistanceTooLarge},
		{append(append(wire.Header(), 0), 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02), wire.ErrVarintOverflow},
		{wire.AppendEnd(wire.AppendLiteral(wire.Header(), []byte("ab")), 3, wire.Checksum([]byte("ab"))), wire.ErrLengthMismatch},
		{wire.AppendEnd(wire.AppendLiteral(wire.Header(), []byte("ab")), 2, 7), wire.ErrChecksumMismatch},
		{append(bytes.Clone(s), 0), wire.ErrTrailingData},
	} {
		_, err := decompress(c.s, 1<<20)
		good = good && errors.Is(err, c.want)
	}
	ok("九类损坏错误", good)
	bomb := wire.AppendBackref(wire.AppendLiteral(wire.Header(), []byte("a")), 1, 1<<40)
	dB, _ := dec.New(dc)
	_, errB := dB.Write(bomb)
	ok("炸弹提前拒绝", errors.Is(errB, wire.ErrOutputLimit) && len(dB.Output()) == 1)
	good = true
	for cut := 0; cut < len(s); cut++ {
		dT, _ := dec.New(dc)
		dT.Write(s[:cut])
		good = good && errors.Is(dT.Close(), wire.ErrTruncated) && bytes.HasPrefix(data, dT.Output())
	}
	ok("截断遍历只出前缀", good)
	good = true
	for i := range s {
		f := bytes.Clone(s)
		f[i] ^= 1 << (i % 8)
		out, err := decompress(f, 1<<20)
		good = good && (err != nil || bytes.Equal(out, data))
	}
	ok("翻转遍历无静默错误", good)
	p1, _ := enc.CompressParallel(data, 8192, 1, ec)
	good = true
	for _, w := range []int{2, 4, 8} {
		p, _ := enc.CompressParallel(data, 8192, w, ec)
		good = good && bytes.Equal(p, p1)
	}
	oP, errP := decompress(p1, 999)
	ok("并行压缩四档相同", good && errP == nil && bytes.Equal(oP, data))
	cand := func(n int) (int64, int) {
		var b bytes.Buffer
		e2, _ := enc.New(&b, ec)
		e2.Write(bytes.Repeat([]byte{5}, n))
		e2.Close()
		return e2.Candidates(), b.Len()
	}
	c64, z64 := cand(64 << 10)
	c4m, z4m := cand(4 << 20)
	ok("考察数对照与长回指", c64*64 <= 2*c4m && c4m <= 128*c64 && z64 < 64<<10 && z4m < 64<<10)
	fmt.Printf("总计: %d 项失败\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
