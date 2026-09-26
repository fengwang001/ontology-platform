// Command demo 逐项判定 AES-128 实现，全部 OK 时退出码为 0。
package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/aes"
	"ontology/api"
	"ontology/gf256"
)

var failed bool

func check(name string, ok bool) {
	st := "OK"
	if !ok {
		st = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, st)
}

func main() {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	pt, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	wantCT, _ := hex.DecodeString("69c4e0d86a7b0430d8cdb78070b4c55a")

	check("GF(2^8) Mul2/Mul3/Mul",
		gf256.Mul2(0xdb) == 0xad && gf256.Mul3(0x13) == 0x35 && gf256.Mul(0x57, 0x83) == 0xc1)

	w, err := aes.ExpandKey(key)
	parts := make([]string, 4)
	for i := 4; i <= 7; i++ {
		parts[i-4] = hex.EncodeToString(w[i][:])
	}
	got := strings.Join(parts, " ")
	check("w[4..7]="+got, err == nil && got == "d6aa74fd d2af72fa daa678f1 d6ab76fe")

	c, err := api.NewCipher(key)
	ct, err2 := c.EncryptBlock(pt)
	check("FIPS-197 测试向量", err == nil && err2 == nil && bytes.Equal(ct, wantCT))

	nt, _ := api.NaiveEncryptBlock(key, pt)
	check("朴素逐轮参照一致", bytes.Equal(nt, ct))

	ac, _ := aes.NewCipher(key)
	act, _ := ac.EncryptBlock(pt)
	check("轮密钥缓存", bytes.Equal(act, ct) && ac.CacheHolds(100))
	check("大m重算轮密钥字数=0", ac.CacheHolds(10000))

	_, e1 := api.NewCipher(key[:15])
	_, e2 := c.EncryptBlock(pt[:15])
	var zero api.Cipher
	_, e3 := zero.EncryptBlock(pt)
	distinct := !errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3)
	check("三类可判定错误", distinct &&
		errors.Is(e1, api.ErrKeyLength) && errors.Is(e2, api.ErrBlockLength) && errors.Is(e3, api.ErrNotInitialized))

	after, _ := c.EncryptBlock(pt)
	check("被拒后状态不变", bytes.Equal(after, ct))

	check("并发一致", concurrentOK(c, key, pt))
	check("SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

// concurrentOK：8 个 goroutine 并发加密各自分组与同一分组，
// 结果须与朴素参照逐字节一致且彼此一致（WaitGroup 同步，不用 sleep）。
func concurrentOK(c *api.Cipher, key, pt []byte) bool {
	const n = 8
	common, err := api.NaiveEncryptBlock(key, pt)
	if err != nil {
		return false
	}
	results := make([][]byte, n)
	commons := make([][]byte, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			blk := make([]byte, 16)
			copy(blk, pt)
			blk[15] ^= byte(i + 1) // 各自不同的分组
			ct, err := c.EncryptBlock(blk)
			if err != nil {
				return
			}
			ref, err := api.NaiveEncryptBlock(key, blk)
			if err != nil || !bytes.Equal(ct, ref) {
				return
			}
			shared, err := c.EncryptBlock(pt)
			if err != nil {
				return
			}
			results[i], commons[i] = ct, shared
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if results[i] == nil || !bytes.Equal(commons[i], common) {
			return false
		}
		for j := i + 1; j < n; j++ {
			if bytes.Equal(results[i], results[j]) { // 不同分组密文应不同
				return false
			}
		}
	}
	return true
}
