package aes

import (
	"bytes"
	"encoding/hex"
	"math/rand"
	"sync"
	"testing"
)

var fipsKey = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func TestFIPS197Vector(t *testing.T) {
	vectors := []struct{ key, pt, ct string }{
		{"000102030405060708090a0b0c0d0e0f", "00112233445566778899aabbccddeeff", "69c4e0d86a7b0430d8cdb78070b4c55a"},
	}
	for _, v := range vectors {
		key, _ := hex.DecodeString(v.key)
		pt, _ := hex.DecodeString(v.pt)
		want, _ := hex.DecodeString(v.ct)
		c, err := NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := c.EncryptBlock(pt); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("密文 %x err=%v，期望 %x", got, err, want)
		}
	}
}

// 钉住 NOTES.md 第三节推导的第一个扩展轮密钥。
func TestExpandKeyFirstWords(t *testing.T) {
	w, err := ExpandKey(fipsKey)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"d6aa74fd", "d2af72fa", "daa678f1", "d6ab76fe"}
	for i, s := range want {
		if got := hex.EncodeToString(w[4+i][:]); got != s {
			t.Fatalf("w[%d]=%s，期望 %s", 4+i, got, s)
		}
	}
}

// naiveRef 朴素参照：逐轮独立调用四个步骤函数，无优化合并。
func naiveRef(t *testing.T, key, block []byte) []byte {
	t.Helper()
	w, err := ExpandKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var s [16]byte
	copy(s[:], block)
	AddRoundKey(&s, w[0:4])
	for r := 1; r < 10; r++ {
		SubBytes(&s)
		ShiftRows(&s)
		MixColumns(&s)
		AddRoundKey(&s, w[4*r:4*r+4])
	}
	SubBytes(&s)
	ShiftRows(&s)
	AddRoundKey(&s, w[40:44])
	return s[:]
}

func TestNaiveReferenceMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(674))
	for i := 0; i < 64; i++ {
		key, blk := make([]byte, BlockSize), make([]byte, BlockSize)
		rng.Read(key)
		rng.Read(blk)
		c, err := NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.EncryptBlock(blk)
		if err != nil {
			t.Fatal(err)
		}
		if want := naiveRef(t, key, blk); !bytes.Equal(got, want) {
			t.Fatalf("用例 %d：密文 %x，朴素参照 %x", i, got, want)
		}
	}
}

// 同一密钥加密 m 个分组，每次加密重新计算的轮密钥字数恒为 0。
func TestRoundKeyCacheZeroReexpand(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c, err := NewCipher(fipsKey)
		if err != nil {
			t.Fatal(err)
		}
		blk := make([]byte, BlockSize)
		for i := 0; i < m; i++ {
			blk[0] = byte(i)
			if _, err := c.EncryptBlock(blk); err != nil {
				t.Fatal(err)
			}
			if n := c.expWords.Load(); n != 0 {
				t.Fatalf("m=%d 第 %d 次加密重算轮密钥字数=%d，应恒为 0", m, i, n)
			}
		}
		if !c.CacheHolds(m) {
			t.Fatalf("m=%d：CacheHolds=false", m)
		}
	}
}

// N 个 goroutine 并发加密各自分组，结果与串行参照逐字节一致。
func TestConcurrentEncryptConsistency(t *testing.T) {
	c, err := NewCipher(fipsKey)
	if err != nil {
		t.Fatal(err)
	}
	const g, per = 16, 64
	blocks := make([][]byte, g*per)
	want := make([][]byte, g*per)
	rng := rand.New(rand.NewSource(1))
	for i := range blocks {
		blocks[i] = make([]byte, BlockSize)
		rng.Read(blocks[i])
		if want[i], err = c.EncryptBlock(blocks[i]); err != nil {
			t.Fatal(err)
		}
	}
	got := make([][]byte, g*per)
	var wg sync.WaitGroup
	for j := 0; j < g; j++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			for k := 0; k < per; k++ {
				i := j*per + k
				ct, err := c.EncryptBlock(blocks[i])
				if err != nil {
					t.Error(err)
					return
				}
				got[i] = ct
			}
		}(j)
	}
	wg.Wait()
	for i := range blocks {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("分组 %d 并发结果与串行参照不一致", i)
		}
	}
}
