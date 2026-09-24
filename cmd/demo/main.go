// 演示程序：不读参数、不联网；退出码 0 当且仅当全部判定通过。输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/kv"
	"ontology/schema"
)

var fails int

var errBoom = errors.New("boom")

func ck(name string, ok bool) {
	fmt.Printf(map[bool]string{true: "OK ", false: "FAIL "}[ok]+"%s\n", name)
	if !ok {
		fails++
	}
}

func m1(b []byte) ([]byte, error) { return append(append([]byte(nil), b...), ',', '2'), nil }
func m2(b []byte) ([]byte, error) {
	if len(b) >= 3 && string(b[:3]) == "bad" {
		return nil, errBoom
	}
	for i := range b {
		if b[i] == ',' {
			b[i] = ';'
		}
	}
	return b, nil
}
func m3(b []byte) ([]byte, error) { return append([]byte("4:"), b...), nil }

func main() {
	// 1-5：第三节十二步；只迁存储版本之后的步骤；写回一次；失败不留痕；缺迁移零调用。
	a := api.New()
	var c [3]int64
	reg := func(from int, f func([]byte) ([]byte, error)) {
		if e := a.Register(from, func(b []byte) ([]byte, error) { atomic.AddInt64(&c[from-1], 1); return f(b) }); e != nil {
			panic(e)
		}
	}
	reg(1, m1)
	reg(2, m2)
	a.Write("k1", []byte("a"))
	a.Write("k2", []byte("bad"))
	_ = a.Upgrade(3)
	a.Write("k3", []byte("c,d"))
	r5, e5 := a.Read("k1")
	_, e6 := a.Read("k2")
	_ = a.Upgrade(4)
	_, e8 := a.Read("k1") // 缺 m3：整链预检失败，一个函数都不调用
	c8 := c
	reg(3, m3) // 步骤9：补登记后同一次读取即成功
	r10, _ := a.Read("k1")
	r11, _ := a.Read("k3")
	r12, _ := a.Read("k1")
	v2, raw2, _ := a.Stored("k2")
	ck("twelve steps: values & storage", e5 == nil && string(r5) == "a;2" &&
		errors.Is(e6, schema.ErrMigrationFailed) && errors.Is(e8, schema.ErrMissingMigration) &&
		string(r10) == "4:a;2" && string(r11) == "4:c,d" && string(r12) == "4:a;2" && v2 == 1 && string(raw2) == "bad")
	ck("failure class+raw cause; missing zero calls", errors.Is(e6, errBoom) && c8 == [3]int64{2, 2, 0})
	ck("writeback once: calls [2 2 2]; self-check ok", c == [3]int64{2, 2, 2} && api.New().SelfCheck() == nil)

	// 6：哨兵互不相同、均可 errors.Is 判定，拒绝后版本仍是 4（状态不变、仍可继续用）。
	bv, br, _, nk := a.Upgrade(1), a.Register(1, m1), error(nil), func() error { _, e := a.Read("zz"); return e }()
	sent := []error{kv.ErrInvalidVersion, schema.ErrInvalidRegister, kv.ErrKeyNotFound, schema.ErrMissingMigration, schema.ErrMigrationFailed}
	distinct := true
	for i := range sent {
		for j := i + 1; j < len(sent); j++ {
			if errors.Is(sent[i], sent[j]) {
				distinct = false
			}
		}
	}
	ck("four distinct decidable error classes", errors.Is(bv, kv.ErrInvalidVersion) &&
		errors.Is(br, schema.ErrInvalidRegister) && errors.Is(nk, kv.ErrKeyNotFound) && distinct && a.Version() == 4)

	// 7：大 m 下 Upgrade/Read 惰性——只经公开 Stored 抽查（不读内部计数器）。
	b := api.New()
	_ = b.Register(1, m1)
	const M = 10000
	for i := 0; i < M; i++ {
		b.Write(fmt.Sprintf("k%05d", i), []byte("x"))
	}
	_ = b.Upgrade(2)
	probe := func(except string) bool { // 只经公开 Stored 抽查（不读内部计数器）
		for _, i := range []int{0, 1, M / 2, M - 1} {
			k := fmt.Sprintf("k%05d", i)
			if v, _, e := b.Stored(k); k != except && (e != nil || v != 1) {
				return false
			}
		}
		return true
	}
	before := probe("") // 升级后整表仍在 v1（升级未扫描/重写任何键）
	r, _ := b.Read("k05000")
	v, _, _ := b.Stored("k05000")
	ck("upgrade/read stay lazy at m=10000", before && v == 2 && probe("k05000") && string(r) == "x,2")

	// 8：N 个 goroutine 并发读同一旧键；channel 卡住第一条链，GOMAXPROCS(1)+Gosched
	// 确定性地让其余读者在链执行期间挂到同一条在飞链上（无 sleep）。链只跑一次、结果逐字节相同。
	prevP := runtime.GOMAXPROCS(1)
	d := api.New()
	var n1 int64
	release, started, barrier := make(chan struct{}), make(chan struct{}, 1), make(chan struct{})
	_ = d.Register(1, func(x []byte) ([]byte, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		atomic.AddInt64(&n1, 1)
		return append(x, '!'), nil
	})
	d.Write("k", []byte("v"))
	_ = d.Upgrade(2)
	const N = 32
	var wg sync.WaitGroup
	res := make([][]byte, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-barrier; res[i], _ = d.Read("k") }(i)
	}
	go func() { barrier <- struct{}{} }()
	<-started
	close(barrier)
	runtime.Gosched()
	close(release)
	wg.Wait()
	runtime.GOMAXPROCS(prevP)
	same := true
	for i := 1; i < N; i++ {
		same = same && string(res[i]) == string(res[0])
	}
	ck("concurrent reads share one migration", atomic.LoadInt64(&n1) == 1 && same && string(res[0]) == "v!")

	if fails > 0 {
		panic("demo failed")
	}
}
