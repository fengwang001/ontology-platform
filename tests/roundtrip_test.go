package tests

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/dec"
	"ontology/enc"
)

const testWindow = 1 << 16

func compress(t *testing.T, data []byte, chunk int) []byte {
	t.Helper()
	var buf bytes.Buffer
	c, err := enc.New(&buf, enc.Config{Window: testWindow, MaxChain: 32})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(data); i += chunk {
		if _, err := c.Write(data[i:min(i+chunk, len(data))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decompress(stream []byte, maxOut int64) ([]byte, error) {
	d, err := dec.New(dec.Config{Window: testWindow, MaxOutput: maxOut})
	if err != nil {
		return nil, err
	}
	if _, err := d.Write(stream); err != nil {
		return nil, err
	}
	if err := d.Close(); err != nil {
		return nil, err
	}
	return d.Output(), nil
}

func randBytes(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	r.Read(b)
	return b
}

func TestRoundtrip(t *testing.T) {
	inputs := map[string][]byte{
		"empty":    {},
		"same":     bytes.Repeat([]byte("a"), 5000),
		"random":   randBytes(4096, 1),
		"periodic": bytes.Repeat([]byte("abc"), 2000),
		"text":     bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 100),
	}
	for name, data := range inputs {
		var ref []byte
		for _, chunk := range []int{1, 7, max(1, len(data))} {
			got := compress(t, data, chunk)
			if ref == nil {
				ref = got
			} else if !bytes.Equal(ref, got) {
				t.Fatalf("%s: chunk %d 输出不同", name, chunk)
			}
		}
		out, err := decompress(ref, 0)
		if err != nil || !bytes.Equal(out, data) {
			t.Fatalf("%s: 往返失败 err=%v", name, err)
		}
	}
}

func TestOverlapBackref(t *testing.T) {
	for _, dist := range []int{1, 2, 3} {
		data := bytes.Repeat([]byte("abc")[:dist], 4000)
		stream := compress(t, data, 1<<20)
		out, err := decompress(stream, 0)
		if err != nil || !bytes.Equal(out, data) {
			t.Fatalf("dist=%d: 往返失败 err=%v", dist, err)
		}
		if len(stream) > len(data)/10 {
			t.Fatalf("dist=%d: 未用长回指，压缩后 %d 字节", dist, len(stream))
		}
	}
}

func TestFlushPromise(t *testing.T) {
	data := randBytes(3000, 2)
	var buf bytes.Buffer
	c, _ := enc.New(&buf, enc.Config{Window: testWindow, MaxChain: 32})
	c.Write(data[:1000])
	c.Flush()
	d, _ := dec.New(dec.Config{Window: testWindow})
	if _, err := d.Write(buf.Bytes()); err != nil || !bytes.Equal(d.Output(), data[:1000]) {
		t.Fatalf("Flush 后无法还原前 1000 字节: %v", err)
	}
	c.Write(data[1000:])
	c.Flush()
	n := buf.Len()
	c.Flush() // 无新输入，不得产生字节
	if buf.Len() != n {
		t.Fatal("空 Flush 产生了字节")
	}
	c.Close()
	out, err := decompress(buf.Bytes(), 0)
	if err != nil || !bytes.Equal(out, data) {
		t.Fatalf("Flush 后往返失败: %v", err)
	}
}

func TestDecChunking(t *testing.T) {
	data := randBytes(600, 3)
	stream := compress(t, data, 7)
	for split := 0; split <= len(stream); split++ {
		d, _ := dec.New(dec.Config{Window: testWindow})
		d.Write(stream[:split])
		d.Write(stream[split:])
		if err := d.Close(); err != nil || !bytes.Equal(d.Output(), data) {
			t.Fatalf("split=%d: %v", split, err)
		}
	}
}

func TestEmptyVsZeroLength(t *testing.T) {
	stream := compress(t, nil, 1)
	if len(stream) == 0 {
		t.Fatal("空输入压缩结果为空")
	}
	if out, err := decompress(stream, 0); err != nil || len(out) != 0 {
		t.Fatalf("空流解压: out=%d err=%v", len(out), err)
	}
	for _, s := range [][]byte{nil, {0x4C, 0x5A, 0x01}} {
		d, _ := dec.New(dec.Config{})
		d.Write(s)
		if err := d.Close(); !errors.Is(err, dec.ErrTruncated) {
			t.Fatalf("len=%d 应判截断: %v", len(s), err)
		}
	}
}
