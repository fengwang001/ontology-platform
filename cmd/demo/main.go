package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// 第三节八步场景（maxMem=2，A=1 B=2 C=3 D=4）
	s, _ := api.New(2)
	s.Put("k1", 1)
	s.Put("k2", 2)
	s.Put("k1", 3)
	s.Del("k2")
	s.Put("k3", 4)
	v6, ok6, d6, _ := s.Get("k1")
	check("step6 Get(k1)==C", v6 == 3 && ok6 && !d6)
	s.Del("k1")
	_, ok8, d8, _ := s.Get("k1")
	check("step8 Get(k1)==deleted", !ok8 && d8)

	// 「已删除」与「不存在」可区分
	_, okX, dX, _ := s.Get("k9")
	check("deleted != not-exist", d8 && !okX && !dX)

	// Compact：k2 墓碑压住旧值不复活；k3 仍在 memtable；读放大 2→1
	_, _, _, _ = s.Get("zz")
	ampBefore := s.ReadAmp()
	s.Compact()
	_, ok2, d2, _ := s.Get("k2")
	v3, ok3, _, _ := s.Get("k3")
	_, _, _, _ = s.Get("zz")
	check("compact: k2 stays deleted, k3 kept", !ok2 && d2 && ok3 && v3 == 4)
	check("readamp 2 -> 1", ampBefore == 2 && s.ReadAmp() == 1)

	// 三类可判定错误，互不相同
	_, errNew := api.New(0)
	errEmpty := s.Put("", 1)
	errLong := s.Del(string(make([]byte, 65)))
	check("3 distinct sentinel errors",
		errors.Is(errNew, api.ErrBadMaxMem) && errors.Is(errEmpty, api.ErrEmptyKey) &&
			errors.Is(errLong, api.ErrKeyTooLong) &&
			!errors.Is(errEmpty, api.ErrKeyTooLong) && !errors.Is(errLong, api.ErrEmptyKey))

	// 被拒后状态不变
	f, _ := api.New(2)
	f.Put("a", 7)
	_, _, _, _ = f.Get("a")
	ampF := f.ReadAmp()
	f.Put("", 1)
	f.Del(string(make([]byte, 65)))
	f.Del("")
	vA, okA, _, _ := f.Get("a")
	check("rejected ops leave no trace", okA && vA == 7 && f.ReadAmp() == ampF)

	// 大 m 的 SSTable 内定位为 O(1)（计数器非导出，由 lsm 包内测试钉住数值）
	big, _ := api.New(10000)
	for i := 0; i < 10000; i++ {
		big.Put(fmt.Sprintf("k%05d", i), int64(i))
	}
	big.Put("trigger-freeze", 1)
	vB, okB, _, _ := big.Get("k09999")
	check("big-m probe O(1) (see TestProbeCountConstant)", okB && vB == 9999 && big.ReadAmp() == 1)

	// 并发 Put 互不相同的 key，期间并发读已写 key 值一致
	c, _ := api.New(16)
	c.Put("const", 42)
	var wg sync.WaitGroup
	bad := make(chan struct{}, 128)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			c.Put(fmt.Sprintf("g%03d", g), int64(g))
			if v, ok, _, _ := c.Get("const"); ok && v != 42 {
				bad <- struct{}{}
			}
		}(g)
	}
	wg.Wait()
	close(bad)
	consistent := true
	for g := 0; g < 64; g++ {
		if v, ok, _, _ := c.Get(fmt.Sprintf("g%03d", g)); !ok || v != int64(g) {
			consistent = false
		}
	}
	check("concurrent put/get consistent", consistent && len(bad) == 0)

	sc, _ := api.New(4)
	check("SelfCheck", sc.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
