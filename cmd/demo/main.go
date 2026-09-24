// 演示程序：逐条打印 OK/FAIL，任一 FAIL 退出码非 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ontology/api"
	"ontology/check"
	"ontology/seq"
)

var failed bool

func ok(name string, cond bool) {
	s := "OK   "
	if !cond {
		s, failed = "FAIL ", true
	}
	fmt.Println(s + name)
}

func pos(dir string) int64 { st, _ := check.Open(dir); n, _ := st.Read(); return n } // 持久化位点

// demoCheck 核验 check 包：原子写读回、损坏判定。
func demoCheck(dir string) {
	os.MkdirAll(dir, 0o755)
	st, _ := check.Open(dir)
	werr := st.Write(42)
	got, rerr := st.Read()
	ok("check: 原子写读回 next=42", werr == nil && rerr == nil && got == 42)
	os.WriteFile(filepath.Join(dir, "checkpoint"), []byte("bad"), 0o644)
	_, cerr := st.Read()
	ok("check: 损坏 checkpoint 报 ErrCorrupt", errors.Is(cerr, check.ErrCorrupt))
}

// demoSeq 核验 seq 包：顺序发放、崩溃恢复后续发不重复。
func demoSeq(dir string) {
	os.MkdirAll(dir, 0o755)
	st, _ := check.Open(dir)
	a := seq.NewAllocator(st)
	var got []int64
	good := true
	for i := 0; i < 3; i++ {
		n, err := a.Next()
		good = good && err == nil
		got = append(got, n)
	}
	a.SimulateCrash()
	good = good && a.Recover() == nil
	n, err := a.Next()
	ok("seq: 崩溃恢复不重复不空洞 0,1,2,3", good && err == nil && fmt.Sprint(append(got, n)) == "[0 1 2 3]")
}

// demoAPI 核验 api 包：八步序列、失败不留痕、三类错误、大 m 位点、并发、自检。
func demoAPI(dir string) {
	// 第三节八步序列：逐步核对返回值与持久化位点。
	d := filepath.Join(dir, "eight")
	a, err := api.New(d)
	wantRet := []int64{0, 1, 2, 3, -1, -1, 4, 5}
	wantPos := []int64{1, 2, 3, 4, 4, 4, 5, 6}
	good := err == nil
	for i, op := range "nnnncrnn" {
		if op == 'n' {
			n, err := a.Next()
			good = good && err == nil && n == wantRet[i]
		} else if op == 'c' {
			a.SimulateCrash()
		} else {
			good = good && a.Recover() == nil
		}
		good = good && pos(d) == wantPos[i]
	}
	ok("api: 八步序列返回值与位点 {0,1,2,3|4,4|4,5}", good)
	// 持久化失败不留痕：目录只读后 Next 报 ErrPersist，位点与内存 next 不变。
	d2 := filepath.Join(dir, "fail")
	a2, _ := api.New(d2)
	a2.Next()
	a2.Next()
	os.Chmod(d2, 0o500)
	_, perr := a2.Next()
	before := pos(d2)
	os.Chmod(d2, 0o700)
	n3, nerr := a2.Next()
	ok("api: 持久化失败不留痕", errors.Is(perr, api.ErrPersist) && before == 2 && nerr == nil && n3 == 2)
	// 三类可判定错误互不相同。
	distinct := !errors.Is(api.ErrInvalidDir, api.ErrPersist) && !errors.Is(api.ErrPersist, api.ErrCorrupt) && !errors.Is(api.ErrCorrupt, api.ErrInvalidDir)
	_, derr := api.New("")
	ok("api: 三类错误可判定且互不相同", distinct && errors.Is(derr, api.ErrInvalidDir))

	// 大 m 下位点文件恒为 8 字节（单条 int64 而非追加日志），Recover 正常。
	d3 := filepath.Join(dir, "bigm")
	a3, _ := api.New(d3)
	good = true
	for m := 1; m <= 2000 && good; m++ {
		_, err := a3.Next()
		good = err == nil
		if m == 100 || m == 500 || m == 2000 {
			fi, serr := os.Stat(filepath.Join(d3, "checkpoint"))
			a3.SimulateCrash()
			good = good && serr == nil && fi.Size() == 8 && a3.Recover() == nil
		}
	}
	ok("api: m=100..2000 位点恒 8 字节、Recover 正常", good)

	// 并发：8 goroutine 共 1024 次 Next，编号集合恰为 {0..1023}。
	d4 := filepath.Join(dir, "conc")
	a4, _ := api.New(d4)
	const g, per = 8, 128
	ch := make(chan int64, g*per)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				n, _ := a4.Next()
				ch <- n
			}
		}()
	}
	wg.Wait()
	close(ch)
	seen := map[int64]bool{}
	dup := false
	for n := range ch {
		dup = dup || seen[n]
		seen[n] = true
	}
	ok("api: 并发 1024 次无重复无空洞", !dup && len(seen) == g*per)
	ok("api: SelfCheck 四条不变量", a4.SelfCheck() == nil)
}

func main() {
	dir, err := os.MkdirTemp("", "demo")
	if err != nil {
		fmt.Println("FAIL 无法创建临时目录")
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	demoCheck(filepath.Join(dir, "check"))
	demoSeq(filepath.Join(dir, "seq"))
	demoAPI(filepath.Join(dir, "api"))
	if failed {
		os.Exit(1)
	}
}
