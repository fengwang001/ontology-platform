package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"ontology/api"
	"ontology/crc"
	"ontology/poly"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

// 第三节：从 Init 逐位处理 '1'(0x31)，返回 8 次循环后的 crc。
func eightSteps() [8]uint32 {
	var out [8]uint32
	c := crc.Init ^ 0x31
	for k := 0; k < 8; k++ {
		if c&1 != 0 {
			c = poly.IEEE ^ (c >> 1)
		} else {
			c >>= 1
		}
		out[k] = c
	}
	return out
}

// bitLoopsOf 用反射+unsafe 窥视 crc 内部的非导出计数器（不经过任何导出接口）。
func bitLoopsOf(c *api.Checksum) int {
	core := reflect.ValueOf(c).Elem().FieldByName("core")
	core = reflect.NewAt(core.Type(), unsafe.Pointer(core.UnsafeAddr())).Elem()
	f := core.Elem().FieldByName("bitLoops")
	return int(reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int())
}

func main() {
	check("poly 合法性判定", poly.Valid(poly.IEEE) && !poly.Valid(0) &&
		!poly.Valid(0x04C11DB7) && poly.Check(poly.IEEE) == nil && poly.Check(0) == poly.ErrInvalid)

	want8 := [8]uint32{0x7FFFFFE7, 0xD2477CD3, 0x849B3D49, 0xAFF51D84,
		0x57FA8EC2, 0x2BFD4761, 0xF8462090, 0x7C231048}
	check("八行逐位表", eightSteps() == want8)

	known, _ := api.New(poly.IEEE)
	known.Update([]byte("123456789"))
	check("已知向量 cbf43926", known.Value() == 0xCBF43926)

	same, concat := true, true
	data := []byte("table driven crc-32 demo data")
	for cut := 0; cut <= len(data); cut++ {
		a, _ := api.New(poly.IEEE)
		a.Update(data[:cut])
		a.Update(data[cut:])
		ref := crc.New(poly.IEEE)
		ref.UpdateBitwise(data)
		one, _ := api.New(poly.IEEE)
		one.Update(data)
		same = same && a.Value() == ref.Value()
		concat = concat && a.Value() == one.Value()
	}
	check("表驱动==逐位参照", same)
	check("拼接性质", concat)

	_, e1 := api.New(0)
	_, e1b := api.New(0x04C11DB7)
	empty, _ := api.New(poly.IEEE)
	e2 := empty.Verify(0)
	full, _ := api.New(poly.IEEE)
	full.Update([]byte("x"))
	e3 := full.Verify(full.Value() ^ 1)
	distinct := e1 != e2 && e2 != e3 && e1 != e3
	check("三类可判定错误", errors.Is(e1, api.ErrInvalidPoly) && errors.Is(e1b, api.ErrInvalidPoly) &&
		errors.Is(e2, api.ErrEmptyVerify) && errors.Is(e3, api.ErrMismatch) && distinct)

	v, n := full.Value(), full.BytesProcessed()
	_ = full.Verify(v ^ 1)
	check("被拒后状态不变", full.Value() == v && full.BytesProcessed() == n &&
		empty.BytesProcessed() == 0 && full.Verify(v) == nil)

	big, _ := api.New(poly.IEEE)
	buf := make([]byte, 10000)
	rand.New(rand.NewSource(1)).Read(buf)
	big.Update(buf)
	check("大m逐位循环次数为0", bitLoopsOf(big) == 0)

	var ok atomic.Bool
	ok.Store(true)
	shared, _ := api.New(poly.IEEE)
	shared.Update([]byte("shared"))
	want := shared.Value()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(seed int64) {
			defer wg.Done()
			b := make([]byte, 256)
			rand.New(rand.NewSource(seed)).Read(b)
			mine, _ := api.New(poly.IEEE)
			mine.Update(b)
			ref := crc.New(poly.IEEE)
			ref.UpdateBitwise(b)
			if mine.Value() != ref.Value() {
				ok.Store(false)
			}
		}(int64(i))
		go func() {
			defer wg.Done()
			if shared.Value() != want || shared.SelfCheck() != nil {
				ok.Store(false)
			}
		}()
	}
	wg.Wait()
	check("并发一致", ok.Load())

	if failed {
		os.Exit(1)
	}
}
