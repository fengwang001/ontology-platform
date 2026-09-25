package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var fails int

func ok(name string, cond bool) {
	if cond {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		fails++
	}
}

func main() {
	// 1. 第三节八步序列（C=4），逐步核验 connID/世代/结果
	d := api.New(4)
	h0, e1 := d.Open()
	h1, e2 := d.Open()
	h2, e3 := d.Open()
	e4 := d.Close(api.Handle{ID: 1, Gen: 1})
	h5, e5 := d.Open()
	e6 := d.Recv(1, 1, []byte("old"))
	e7 := d.Recv(3, 1, []byte("x"))
	e8 := d.Recv(1, 2, []byte("hi"))
	ok("8-step: H{0,1} H{1,1} H{2,1} close H{1,2} stale half-open deliver",
		e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil &&
			h0.ID == 0 && h0.Gen == 1 && h1.ID == 1 && h1.Gen == 1 &&
			h2.ID == 2 && h2.Gen == 1 && h5.ID == 1 && h5.Gen == 2 &&
			errors.Is(e6, api.ErrStale) && errors.Is(e7, api.ErrHalfOpen) && e8 == nil)

	// 2. 世代隔离：旧世代帧 "old" 不串进新连接，数据只有 "hi"
	ok("gen isolation: no cross-talk", string(d.Data(1)) == "hi")

	// 3. 四类错误互不相同且各自可触发
	errs := []error{api.ErrNoSlots, api.ErrBadID, api.ErrHalfOpen, api.ErrStale}
	distinct := true
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	full := api.New(1)
	full.Open()
	_, noSlot := full.Open()
	ok("4 distinct errors", distinct && errors.Is(noSlot, api.ErrNoSlots) &&
		errors.Is(d.Recv(9, 1, nil), api.ErrBadID) &&
		errors.Is(d.Recv(3, 1, nil), api.ErrHalfOpen) &&
		errors.Is(d.Send(api.Handle{ID: 1, Gen: 1}, nil), api.ErrStale))

	// 4. 被拒后状态不变
	g1, g3, data := d.Gen(1), d.Gen(3), string(d.Data(1))
	d.Recv(99, 1, nil)
	d.Recv(3, 1, nil)
	d.Recv(1, 1, nil)
	d.Close(api.Handle{ID: 1, Gen: 1})
	ok("rejected ops leave no trace", d.Gen(1) == g1 && d.Gen(3) == g3 && string(d.Data(1)) == data)

	// 5. Open 定位代价 ≤ ⌈log2(C)⌉+1（复用密集场景）
	big := api.New(10000)
	var hs []api.Handle
	for i := 0; i < 5000; i++ {
		h, _ := big.Open()
		hs = append(hs, h)
	}
	for _, h := range hs[:2500] {
		big.Close(h)
	}
	costOK := true
	for i := 0; i < 2500; i++ {
		big.Open()
		costOK = costOK && big.CheckOpenCost() == nil
	}
	ok("open cost <= ceil(log2(C))+1", costOK)

	// 6. 并发 Open/Send + 主线程 Recv：句柄两两不同、数据一一对应不串流
	const N = 64
	cd := api.New(N)
	ch := make(chan api.Handle, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := cd.Open()
			if err == nil && cd.Send(h, []byte{byte(i)}) == nil {
				ch <- h
			}
		}(i)
	}
	wg.Wait()
	close(ch)
	seen := map[api.Handle]bool{}
	uniq := true
	var got []api.Handle
	for h := range ch {
		if seen[h] {
			uniq = false
		}
		seen[h] = true
		got = append(got, h)
	}
	cross := true
	for i, h := range got {
		cd.Recv(h.ID, h.Gen, []byte{byte(i)})
	}
	for i, h := range got {
		if string(cd.Data(h.ID)) != string([]byte{byte(i)}) {
			cross = false
		}
	}
	ok("concurrent open/send/recv no cross-talk", len(got) == N && uniq && cross)

	// 7. 内置自检：四条不变量
	ok("SelfCheck", api.New(4).SelfCheck() == nil)

	if fails > 0 {
		fmt.Println("FAIL")
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
