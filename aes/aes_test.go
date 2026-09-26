package aes

import (
	"bytes"
	"encoding/hex"
	"math/rand"
	"sync"
	"testing"

	"ontology/gf256"
)

func hb(s string) []byte { b, _ := hex.DecodeString(s); return b }
func TestGF256(t *testing.T) {
	cases := []struct{ a, m2, m3, b, mul byte }{
		{0xdb, 0xad, 0x76, 0x13, 0x69}, // 2db=ad; 3db=ad^db=76; db·13=69
		{0x57, 0xae, 0xf9, 0x13, 0xfe}, // FIPS: 57·13 = fe
		{0x53, 0xa6, 0xf5, 0xca, 0x01}, // 53 and ca are field inverses
		{0x00, 0x00, 0x00, 0xff, 0x00},
		{0x01, 0x02, 0x03, 0x01, 0x01},
		{0xff, 0xe5, 0x1a, 0x02, 0xe5},
	}
	for _, c := range cases {
		if gf256.Mul2(c.a) != c.m2 || gf256.Mul3(c.a) != c.m3 || gf256.Mul(c.a, c.b) != c.mul {
			t.Fatalf("gf mismatch at %+v", c)
		}
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		a, b := byte(r.Intn(256)), byte(r.Intn(256))
		if gf256.Mul(a, b) != gf256.Mul(b, a) || gf256.Mul(a, 1) != a ||
			gf256.Mul2(a) != gf256.Mul(a, 2) || gf256.Mul3(a) != gf256.Mul(a, 3) {
			t.Fatalf("field law broken at %02x,%02x", a, b)
		}
	}
}
func TestKeyExpansionKnownWords(t *testing.T) {
	w, err := ExpandKey(hb("000102030405060708090a0b0c0d0e0f"))
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0xd6aa74fd, 0xd2af72fa, 0xdaa678f1, 0xd6ab76fe}
	for i, x := range want {
		if w[4+i] != x {
			t.Errorf("w[%d]=%08x want %08x", 4+i, w[4+i], x)
		}
	}
}
func TestFIPS197Vector(t *testing.T) {
	c, _ := New(hb("000102030405060708090a0b0c0d0e0f"))
	ct, err := c.EncryptBlock(hb("00112233445566778899aabbccddeeff"))
	if err != nil {
		t.Fatal(err)
	}
	if want := hb("69c4e0d86a7b0430d8cdb78070b4c55a"); !bytes.Equal(ct, want) {
		t.Fatalf("ct=%x want %x", ct, want)
	}
}
func TestNaiveReference(t *testing.T) {
	keys := []string{
		"00000000000000000000000000000000",
		"000102030405060708090a0b0c0d0e0f",
		"ffffffffffffffffffffffffffffffff",
	}
	check := func(t *testing.T, key, p []byte, i int) {
		t.Helper()
		c, _ := New(key)
		got, e1 := c.EncryptBlock(p)
		want, e2 := NaiveEncrypt(key, p)
		if e1 != nil || e2 != nil || !bytes.Equal(got, want) {
			t.Fatalf("case %d mismatch key=%x p=%x got=%x want=%x", i, key, p, got, want)
		}
	}
	for i, ks := range keys {
		p := make([]byte, 16)
		for j := range p {
			p[j] = byte(i*31 + j*17)
		}
		check(t, hb(ks), p, i)
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		key, p := make([]byte, 16), make([]byte, 16)
		r.Read(key)
		r.Read(p)
		check(t, key, p, 100+i)
	}
}
func TestRoundKeyCacheCounter(t *testing.T) {
	c, _ := New(hb("000102030405060708090a0b0c0d0e0f"))
	for _, m := range []int{100, 1000, 10000} {
		p := make([]byte, 16)
		for i := 0; i < m; i++ {
			p[i%16] = byte(1 + i)
			if _, err := c.EncryptBlock(p); err != nil {
				t.Fatal(err)
			}
			if n := c.words.Load(); n != 0 { // white-box read; never exposed by any API
				t.Fatalf("m=%d block %d recomputed %d words", m, i, n)
			}
		}
	}
}
func TestRejectedEncryptLeavesCounterUntouched(t *testing.T) {
	c, _ := New(hb("000102030405060708090a0b0c0d0e0f"))
	if _, err := c.EncryptBlock(make([]byte, 15)); err != errBadBlock {
		t.Fatalf("err=%v want errBadBlock", err)
	}
	if n := c.words.Load(); n != 0 {
		t.Fatalf("counter changed after rejection: %d", n)
	}
}
func TestExpandKeyRejectsBadLength(t *testing.T) {
	for _, n := range []int{0, 1, 15, 17, 32} {
		if _, err := ExpandKey(make([]byte, n)); err != errBadKey {
			t.Fatalf("len=%d err=%v want errBadKey", n, err)
		}
	}
}
func TestConcurrentEncrypt(t *testing.T) {
	key := hb("000102030405060708090a0b0c0d0e0f")
	c, _ := New(key)
	const n = 64
	var wg sync.WaitGroup
	results := make([][]byte, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			p := make([]byte, 16)
			for i := range p {
				p[i] = byte(g*17 + i*3)
			}
			got, err := c.EncryptBlock(p)
			want, _ := NaiveEncrypt(key, p)
			again, _ := c.EncryptBlock(p)
			if err != nil || !bytes.Equal(got, want) || !bytes.Equal(got, again) {
				t.Errorf("goroutine %d mismatch", g)
				return
			}
			results[g] = got
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		if len(results[g]) != 16 {
			t.Fatalf("missing result for %d", g)
		}
	}
}
