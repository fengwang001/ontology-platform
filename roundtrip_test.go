package ontology_test

import "bytes"
import "math/rand"
import "testing"
import "ontology/dec"
import "ontology/enc"
import "ontology/wire"

var cfg = enc.Config{Window: 1 << 20, MaxChain: 128}

func dcfg() dec.Config { return dec.Config{Window: 1 << 20, MaxOutput: 1 << 30} }

func samples() map[string][]byte {
	rnd := rand.New(rand.NewSource(1))
	r := make([]byte, 1<<16+123)
	rnd.Read(r)
	mixed := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 1500)
	mixed = append(mixed, r[:5000]...)
	return map[string][]byte{
		"same":     bytes.Repeat([]byte{0xAB}, 70000),
		"random":   r,
		"periodic": bytes.Repeat([]byte("abc"), 15000),
		"mixed":    mixed,
	}
}

func encode(t *testing.T, data []byte, chunk int, flushes ...int) []byte {
	t.Helper()
	var buf bytes.Buffer
	e, err := enc.New(&buf, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fi, off := 0, 0
	for off < len(data) {
		n := min(chunk, len(data)-off)
		if _, err := e.Write(data[off : off+n]); err != nil {
			t.Fatal(err)
		}
		off += n
		for fi < len(flushes) && flushes[fi] == off {
			if err := e.Flush(); err != nil {
				t.Fatal(err)
			}
			fi++
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, stream []byte, chunk int) ([]byte, error) {
	t.Helper()
	d, _ := dec.New(dcfg())
	for off := 0; off < len(stream); off += chunk {
		n := min(chunk, len(stream)-off)
		if _, err := d.Write(stream[off : off+n]); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}
func TestRoundTrip(t *testing.T) {
	for name, data := range samples() {
		stream := encode(t, data, 4096)
		if out, err := decode(t, stream, 1024); err != nil || !bytes.Equal(out, data) {
			t.Errorf("%s: 往返失败 err=%v", name, err)
		}
	}
}
func TestOverlapBackref(t *testing.T) {
	for _, dist := range []int{1, 2, 3} {
		want := []byte("abc")[:dist]
		for len(want) < dist+1000 { // 逐字节向前复制语义
			want = append(want, want[len(want)-dist])
		}
		stream := wire.AppendLiteral(wire.Header(), want[:dist])
		stream = wire.AppendBackref(stream, dist, 1000)
		stream = wire.AppendEnd(stream, uint64(len(want)), wire.Checksum(want))
		if out, err := decode(t, stream, 5); err != nil || !bytes.Equal(out, want) {
			t.Errorf("dist=%d: 重叠回指还原失败 err=%v", dist, err)
		}
	}
}
func TestWriteChunking(t *testing.T) {
	data := samples()["mixed"]
	base := []byte(nil)
	check := func(chunk int, flushes ...int) {
		got := encode(t, data, chunk, flushes...)
		if base == nil {
			base = got
		} else if !bytes.Equal(got, base) {
			t.Errorf("chunk=%d flush=%v 输出不同", chunk, flushes)
		}
	}
	for _, c := range []int{1, 7, 100, 1 << 20} {
		check(c)
	}
	base = nil
	for _, c := range []int{1, 7, 100} {
		check(c, 7000, 21000)
	}
}
func TestFlushPromise(t *testing.T) {
	data := samples()["mixed"]
	var buf bytes.Buffer
	e, _ := enc.New(&buf, cfg)
	e.Write(data[:5000])
	e.Flush()
	if n := buf.Len(); e.Flush() != nil || buf.Len() != n {
		t.Error("连续第二次 Flush 产生了字节")
	}
	d, _ := dec.New(dcfg())
	d.Write(buf.Bytes())
	if !bytes.Equal(d.Output(), data[:5000]) {
		t.Error("Flush 后不能还原截至 Flush 的输入")
	}
	e.Write(data[5000:])
	e.Close()
	if out, err := decode(t, buf.Bytes(), 1<<20); err != nil || !bytes.Equal(out, data) {
		t.Error("Flush 后完整流往返失败")
	}
}
func TestDecodeChunking(t *testing.T) {
	data := samples()["periodic"]
	stream := encode(t, data, 4096)
	for _, chunk := range []int{1, 3, 7, 64, len(stream)} {
		if out, err := decode(t, stream, chunk); err != nil || !bytes.Equal(out, data) {
			t.Errorf("chunk=%d: 解压结果不同", chunk)
		}
	}
}
func TestEmptyVsTruncated(t *testing.T) {
	if stream := encode(t, nil, 1); len(stream) == 0 {
		t.Fatal("空输入的压缩结果必须是非空合法流")
	} else if out, err := decode(t, stream, 1); err != nil || len(out) != 0 {
		t.Errorf("空流应解出长度 0: err=%v len=%d", err, len(out))
	}
	for _, s := range [][]byte{{}, wire.Header()} { // 零长流与秃头流
		d, _ := dec.New(dcfg())
		d.Write(s)
		if err := d.Close(); err == nil {
			t.Error("截断流被误判为合法")
		}
	}
}
