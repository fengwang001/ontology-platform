package enc_test

import (
	"bytes"
	"math/rand"
	"testing"

	"ontology/dec"
	"ontology/enc"
)

var cfg = enc.Config{Window: 1 << 16, Chain: 128}

// compress 按 chunk 大小写 data，并在 flushes 指定的累计偏移处 Flush。
func compress(t *testing.T, data []byte, chunk int, flushes ...int) []byte {
	t.Helper()
	var buf bytes.Buffer
	e, err := enc.New(&buf, cfg)
	if err != nil {
		t.Fatal(err)
	}
	atFlush := func(off int) bool {
		for _, f := range flushes {
			if f == off {
				return true
			}
		}
		return false
	}
	for pos := 0; pos < len(data); {
		n := chunk
		for _, f := range flushes {
			if f > pos && f-pos < n {
				n = f - pos
			}
		}
		if n > len(data)-pos {
			n = len(data) - pos
		}
		if _, err := e.Write(data[pos : pos+n]); err != nil {
			t.Fatal(err)
		}
		pos += n
		if atFlush(pos) {
			if err := e.Flush(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, stream []byte) ([]byte, error) {
	t.Helper()
	d, _ := dec.New(0, 1<<16)
	if _, err := d.Write(stream); err != nil {
		return nil, err
	}
	return d.Output(), d.Close()
}

func inputs() map[string][]byte {
	rnd := rand.New(rand.NewSource(2))
	r := make([]byte, 100000)
	rnd.Read(r)
	return map[string][]byte{
		"empty":    nil,
		"same":     bytes.Repeat([]byte{7}, 100000),
		"random":   r,
		"period2":  bytes.Repeat([]byte("ab"), 50000),
		"periodic": bytes.Repeat([]byte("abc"), 40000),
		"text":     bytes.Repeat([]byte("the quick brown fox "), 5000),
	}
}

func TestRoundTrip(t *testing.T) {
	for name, data := range inputs() {
		out, err := decode(t, compress(t, data, 1<<20))
		if err != nil || !bytes.Equal(out, data) {
			t.Errorf("%s: roundtrip failed, err=%v len=%d/%d", name, err, len(out), len(data))
		}
	}
}

func TestWriteChunking(t *testing.T) {
	data := inputs()["text"][:840]
	flushes := []int{210, 630}
	base := compress(t, data, 1, flushes...)
	for _, chunk := range []int{7, 30, 840} {
		if got := compress(t, data, chunk, flushes...); !bytes.Equal(got, base) {
			t.Errorf("chunk %d: stream differs", chunk)
		}
	}
}

func TestFlush(t *testing.T) {
	var buf bytes.Buffer
	e, _ := enc.New(&buf, cfg)
	p1 := []byte("hello ")
	p2 := []byte("world world world")
	e.Write(p1)
	e.Flush()
	d, _ := dec.New(0, 1<<16)
	d.Write(buf.Bytes()) // 不 Close：Flush 的字节必须足以还原 p1
	if !bytes.Equal(d.Output(), p1) {
		t.Fatalf("after flush: got %q", d.Output())
	}
	n := buf.Len()
	e.Flush() // 无新输入，第二次 Flush 不得产生字节
	if buf.Len() != n {
		t.Fatalf("empty flush produced %d bytes", buf.Len()-n)
	}
	e.Write(p2)
	e.Close()
	out, err := decode(t, buf.Bytes())
	if err != nil || !bytes.Equal(out, append(p1, p2...)) {
		t.Fatalf("final: err=%v out=%q", err, out)
	}
}

func TestParallel(t *testing.T) {
	data := append(bytes.Repeat([]byte("abc"), 50000), inputs()["random"]...)
	base, err := enc.CompressParallel(data, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []int{2, 4, 8} {
		got, _ := enc.CompressParallel(data, 4096, w)
		if !bytes.Equal(got, base) {
			t.Fatalf("workers=%d differs", w)
		}
	}
	for i := 0; i < 30; i++ { // 确定性：同输入 30 次逐字节相同
		got, _ := enc.CompressParallel(data, 4096, 4)
		if !bytes.Equal(got, base) {
			t.Fatal("nondeterministic")
		}
	}
	out, err := decode(t, base) // 并行结果可被流式解压器直接解开
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("roundtrip: err=%v", err)
	}
}

func TestCompressBound(t *testing.T) {
	s := compress(t, make([]byte, 4<<20), 1<<20)
	if len(s) >= 64<<10 {
		t.Fatalf("4MB same-byte compressed to %d bytes", len(s))
	}
	t.Logf("4MB same-byte compressed to %d bytes", len(s))
}

func TestBadConfig(t *testing.T) {
	var buf bytes.Buffer
	for _, c := range []enc.Config{{Window: 0, Chain: 1}, {Window: 1, Chain: 0}} {
		if _, err := enc.New(&buf, c); err != enc.ErrBadConfig {
			t.Errorf("cfg %+v: got %v", c, err)
		}
	}
	if _, err := enc.CompressParallel([]byte("x"), 0, 1); err != enc.ErrBadConfig {
		t.Errorf("blockSize 0: got %v", err)
	}
	if _, err := enc.CompressParallel([]byte("x"), 1, 0); err != enc.ErrBadConfig {
		t.Errorf("workers 0: got %v", err)
	}
}
