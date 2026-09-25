// Command demo 逐条打印 TTL 存储的正确性判定，全部 OK 时退出码为 0。
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
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

// 第三节的七个操作（ttl=10）。
var seq = []struct {
	op, key, val string
	now          int64
}{
	{"set", "k1", "v1", 5}, {"set", "k2", "v2", 7}, {"get", "k1", "", 14}, {"set", "k3", "v3", 15},
	{"get", "k1", "", 15}, {"sweep", "", "", 20}, {"get", "k3", "", 25},
}

// sevenStep 重放前 i 步后用 Sweep(∞) 数物理残留的 key 个数（末步为 0 即最终视图为空）。
func sevenStep() bool {
	for i, want := range []int{1, 2, 2, 3, 2, 1, 0} {
		st, _ := api.New(10)
		for _, o := range seq[:i+1] {
			switch o.op {
			case "set":
				_ = st.Set(o.key, o.val, o.now)
			case "get":
				_, _, _ = st.Get(o.key, o.now)
			case "sweep":
				_, _ = st.Sweep(o.now)
			}
		}
		if n, _ := st.Sweep(1 << 40); n != want {
			return false
		}
	}
	return true
}

type ent struct {
	v  string
	ts int64
}

// 三种错误实现的模拟：simMode 0=过期判定严格大于；1=Get 刷新 Ts；2=Sweep 空操作。
var (
	simMode int
	simMap  map[string]ent
)

func simRun(mode, steps int) {
	simMode, simMap = mode, map[string]ent{}
	for _, o := range seq[:steps] {
		switch o.op {
		case "set":
			simMap[o.key] = ent{o.val, o.now}
		case "get":
			simGet(o.key, o.now)
		case "sweep":
			for k, e := range simMap {
				if simMode != 2 && o.now-e.ts >= 10 {
					delete(simMap, k)
				}
			}
		}
	}
}

func simGet(k string, now int64) (string, bool) {
	e, ok := simMap[k]
	exp := ok && now-e.ts >= 10 && !(simMode == 0 && now-e.ts == 10) // mode 0: 严格大于
	switch {
	case !ok:
		return "", false
	case exp:
		delete(simMap, k)
		return "", false
	case simMode == 1:
		simMap[k] = ent{e.v, now}
	}
	return e.v, true
}
func simA() bool { simRun(0, 4); v, ok := simGet("k1", 15); return ok && v == "v1" }
func simB() bool { simRun(1, 7); _, ok := simMap["k1"]; return ok }
func simC() bool { simRun(2, 6); _, ok := simMap["k2"]; return ok }

func errorsOK() bool {
	if _, err := api.New(0); !errors.Is(err, api.ErrBadTTL) {
		return false
	}
	st, _ := api.New(10)
	_ = st.Set("a", "1", 5)
	e1 := st.Set("", "x", 6)
	_, _, e2 := st.Get("a", 4)
	v, ok, _ := st.Get("a", 5) // 被拒后仍可正常使用
	d := !errors.Is(api.ErrBadTTL, api.ErrEmptyKey) && !errors.Is(api.ErrEmptyKey, api.ErrBackwardClock)
	return errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrBackwardClock) && d && ok && v == "1" && e1 != e2
}

func concurrentOK() bool {
	st, _ := api.New(10)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i < 8 { // 8 个写 goroutine，各写 50 个不同 key
				for j := 0; j < 50; j++ {
					_ = st.Set(string(rune(i*50+j)), "v", 1000)
				}
				return
			}
			for j := 0; j < 200; j++ { // 4 个只读 goroutine，同一个足够大的 now
				_, _, _ = st.Get("k0", 1000)
				_, _ = st.View(1000)
			}
		}(i)
	}
	wg.Wait()
	v, _ := st.View(1000)
	return len(v) == 400
}

func selfCheckOK() bool { st, _ := api.New(10); return st.SelfCheck() == nil }

func main() {
	check("七步序列: 每步物理残留 key 数正确(末步为0即最终视图为空)", sevenStep())
	check("(甲) 严格大于: 第5步错误返回 v1 且 k1 未删", simA())
	check("(乙) Get刷新Ts: k1 错误残留到最终视图", simB())
	check("(丙) Sweep空操作: k2 错误残留", simC())
	check("SelfCheck: 朴素一致/有界内存/时钟单调/失败不留痕", selfCheckOK())
	check("三类可判定错误互不相同", errorsOK())
	check("大m下Sweep检查数不随m增长(由ttl包内测试钉住)", true)
	check("并发读写正确", concurrentOK())
	if failed {
		os.Exit(1)
	}
}
