package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/crc"
	"ontology/poly"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

// eightSteps 返回初值 0xFFFFFFFF 处理首字节 '1' 的 8 次逐位循环后的 crc。
func eightSteps() [8]uint32 {
	var out [8]uint32
	c := uint32(0xFFFFFFFF) ^ 0x31
	for i := 0; i < 8; i++ {
		if c&1 != 0 {
			c = poly.IEEE ^ (c >> 1)
		} else {
			c >>= 1
		}
		out[i] = c
	}
	return out
}

func concurrent() bool {
	const n = 8
	tab := crc.NewTable(poly.IEEE)
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	for g := 0; g < n; g++ { // N 个写者：随机数据 Update 后须与逐位参照相同
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			buf := make([]byte, 1+r.Intn(512))
			r.Read(buf)
			c, _ := api.New(poly.IEEE)
			c.Update(buf)
			d := crc.NewDigest(tab, 0xFFFFFFFF)
			d.UpdateBitwise(buf)
			if c.Value() != d.Raw()^0xFFFFFFFF {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}(int64(g))
	}
	full, _ := api.New(poly.IEEE) // N 个读者：同一实例的 Value 必须完全相同
	full.Update([]byte("shared-instance"))
	want := full.Value()
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if full.Value() != want || full.BytesProcessed() != 15 {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return ok
}

func main() {
	check("poly: 合法性判定与位反射", poly.Valid(poly.IEEE) && !poly.Valid(0) &&
		!poly.Valid(0x04C11DB7) && poly.Reflect(0x04C11DB7) == poly.IEEE)

	steps := eightSteps()
	check(fmt.Sprintf("crc: 八次逐位循环 %08X %08X %08X %08X %08X %08X %08X %08X",
		steps[0], steps[1], steps[2], steps[3], steps[4], steps[5], steps[6], steps[7]),
		steps == [8]uint32{0x7FFFFFE7, 0xD2477CD3, 0x849B3D49, 0xAFF51D84,
			0x57FA8EC2, 0x2BFD4761, 0xF8462090, 0x7C231048})

	tab := crc.NewTable(poly.IEEE)
	d1, d2 := crc.NewDigest(tab, 0xFFFFFFFF), crc.NewDigest(tab, 0xFFFFFFFF)
	d1.UpdateTable([]byte("123456789"))
	d2.UpdateBitwise([]byte("123456789"))
	check("crc: 表驱动与逐位参照一致", d1.Raw() == d2.Raw())

	d3 := crc.NewDigest(tab, 0xFFFFFFFF)
	d3.UpdateTable(make([]byte, 10000))
	check("crc: 大 m 下逐位循环次数为 0", d3.TableDriven())

	c, err := api.New(poly.IEEE)
	check("api: 已知向量 cbf43926", err == nil && func() bool {
		c.Update([]byte("123456789"))
		return c.Value() == 0xCBF43926
	}())

	e1, e2 := func() error { _, e := api.New(0); return e }(), func() error {
		v, _ := api.New(poly.IEEE)
		return v.Verify(0)
	}()
	e3 := c.Verify(0)
	check("api: 三类可判定错误互不相同",
		errors.Is(e1, api.ErrInvalidPoly) && errors.Is(e2, api.ErrEmptyVerify) &&
			errors.Is(e3, api.ErrMismatch) &&
			!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))

	before := c.Value()
	c.Verify(before ^ 1)
	check("api: 被拒后状态不变", c.Value() == before && c.BytesProcessed() == 9)
	check("api: SelfCheck 通过", api.SelfCheck() == nil)
	check("api: 并发读写一致", concurrent())

	if failed {
		os.Exit(1)
	}
}
