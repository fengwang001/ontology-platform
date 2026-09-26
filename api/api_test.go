package api_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

var (
	testKey = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	testBlk = []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
)

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 三类故障注入各有可判定且互不相同的哨兵错误。
func TestSentinelErrorsDistinct(t *testing.T) {
	c, err := api.NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	var zero api.Cipher
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"密钥长度非法", func() error { _, e := api.NewCipher(testKey[:15]); return e }, api.ErrKeyLength},
		{"分组长度非法", func() error { _, e := c.EncryptBlock(testBlk[:15]); return e }, api.ErrBlockLength},
		{"未初始化", func() error { _, e := zero.EncryptBlock(testBlk); return e }, api.ErrNotInitialized},
	}
	for _, tc := range cases {
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Fatalf("%s：err=%v，期望 %v", tc.name, err, tc.want)
		}
	}
	sentinels := []error{api.ErrKeyLength, api.ErrBlockLength, api.ErrNotInitialized}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Fatalf("哨兵错误 %v 与 %v 未区分", a, b)
			}
		}
	}
}

// 不变量 4：任何被拒操作不改变状态，之后仍可正常使用。
func TestFailureLeavesNoTrace(t *testing.T) {
	c, err := api.NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.EncryptBlock(testBlk)
	if err != nil {
		t.Fatal(err)
	}
	var zero api.Cipher
	rejected := []func() error{
		func() error { _, e := api.NewCipher(testKey[:15]); return e },
		func() error { _, e := api.NewCipher(make([]byte, 17)); return e },
		func() error { _, e := c.EncryptBlock(testBlk[:15]); return e },
		func() error { _, e := c.EncryptBlock(nil); return e },
		func() error { _, e := zero.EncryptBlock(testBlk); return e },
	}
	for i, run := range rejected {
		if run() == nil {
			t.Fatalf("第 %d 个被拒操作应返回错误", i)
		}
	}
	after, err := c.EncryptBlock(testBlk)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("被拒操作改变了状态：before=%x after=%x err=%v", before, after, err)
	}
	if _, err := zero.EncryptBlock(testBlk); !errors.Is(err, api.ErrNotInitialized) {
		t.Fatal("零值 cipher 应仍被拒绝且状态不变")
	}
	if _, err := api.NewCipher(testKey); err != nil { // 正常路径不受影响
		t.Fatal(err)
	}
}

// EncryptBlock 与 SelfCheck 可被多 goroutine 并发调用（WaitGroup 同步）。
func TestConcurrentSelfCheckAndEncrypt(t *testing.T) {
	c, err := api.NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	want, err := c.EncryptBlock(testBlk)
	if err != nil {
		t.Fatal(err)
	}
	const n = 16
	errs := make(chan error, 2*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				errs <- api.SelfCheck()
				return
			}
			ct, err := c.EncryptBlock(testBlk)
			if err == nil && !bytes.Equal(ct, want) {
				t.Error("并发加密结果与串行不一致")
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}
