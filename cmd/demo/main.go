// Command demo 逐条演示分层合并 KV 写入路径的判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/seg"
	"ontology/tree"
)

var failed bool

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Println(status, name)
}

// runSeq 执行第三节 17 步序列（p=Put d=Del g=Get，键值均单字符），返回各 Get 结果与第 11 步后的段快照。
func runSeq() (gets []string, after11 []seg.Segment) {
	tr, _ := tree.New(2, 2, 1)
	seq := "pa1 pb2 pc3 gc pa9 pd4 db pe5 gb pf6 pg7 gb ga pb8 ph1 gb gh"
	for i, tok := range strings.Fields(seq) {
		switch tok[0] {
		case 'p':
			_ = tr.Put(tok[1:2], tok[2:])
		case 'd':
			_ = tr.Del(tok[1:2])
		case 'g':
			v, ok := tr.Get(tok[1:2])
			if !ok {
				v = "不存在"
			}
			gets = append(gets, v)
		}
		if i == 10 { // 第 11 步（下标 10）级联后
			after11 = tr.Segments()
		}
	}
	return gets, after11
}

func main() {
	// 段编码：截断 1 字节恢复 2 条丢 1 条；非法 tag / 首条不完整报 ErrCorrupt。
	s := seg.Segment{ID: 1, Recs: []seg.Record{{Key: "a", Val: "1"}, {Key: "b", Val: "2"}, {Key: "c", Del: true}}}
	enc := seg.Encode(s)
	got, dropped, err := seg.LoadSegment(enc[:len(enc)-1])
	_, _, err1 := seg.LoadSegment([]byte{1, 'a', 9, 0, 0}) // 非法 tag
	_, _, err2 := seg.LoadSegment(enc[:1])                 // 首条即不完整
	check("段编解码:截断恢复+损坏报错", err == nil && dropped == 1 && len(got.Recs) == 2 &&
		errors.Is(err1, seg.ErrCorrupt) && errors.Is(err2, seg.ErrCorrupt))
	// 第三节序列：第 4/9/12/13/16/17 步 Get = 3/不存在/不存在/9/8/1。
	gets, after11 := runSeq()
	check("第三节序列Get判定", fmt.Sprint(gets) == fmt.Sprint([]string{"3", "不存在", "不存在", "9", "8", "1"}))
	// 级联后段分布：L0 空、L1 一段 6 条记录（含保留的墓碑 −b）。
	dist := len(after11) == 1 && after11[0].Level == 1 && len(after11[0].Recs) == 6
	// 墓碑在无更早 Put 时被丢弃：Del(z) 先于任何 Put(z)，合并后段中无 z。
	tr, _ := tree.New(1, 2, 1)
	_ = tr.Del("z")
	_ = tr.Put("y", "1")
	_ = tr.Put("x", "1")
	segs := tr.Segments()
	_, zOK := segs[0].Latest("z")
	check("级联段分布+墓碑丢弃", dist && len(segs) == 1 && len(segs[0].Recs) == 1 && !zOK)
	// 与批量参照一致：确定性伪随机操作序列 vs 朴素 map。
	eng, _ := api.New(3, 3, 2)
	ref := map[string]string{}
	seed := uint64(7)
	rnd := func() uint64 { seed = seed*6364136223846793005 + 1442695040888963407; return seed >> 33 }
	for i := 0; i < 1500; i++ {
		k := fmt.Sprint("k", rnd()%30)
		if rnd()%3 == 0 {
			_ = eng.Del(k)
			delete(ref, k)
			continue
		}
		v := fmt.Sprint("v", rnd()%500)
		_ = eng.Put(k, v)
		ref[k] = v
	}
	consistent := true
	for i := 0; i < 35; i++ {
		v, ok := eng.Get(fmt.Sprint("k", i))
		rv, rok := ref[fmt.Sprint("k", i)]
		consistent = consistent && ok == rok && (!ok || v == rv)
	}
	check("与批量参照一致", consistent)
	// 三类可判定错误互不相同；被拒后状态不变、可继续用。
	_, e1 := api.New(0, 2, 1)
	eng2, _ := api.New(2, 2, 1)
	_ = eng2.Put("a", "1")
	e2 := eng2.Put("", "x")
	_ = eng2.Del("")
	_, _, e3 := seg.LoadSegment([]byte{1, 'k', 9, 0})
	distinct := errors.Is(e1, api.ErrParam) && errors.Is(e2, api.ErrEmptyKey) && errors.Is(e3, api.ErrCorrupt) &&
		!errors.Is(e1, api.ErrEmptyKey) && !errors.Is(e2, api.ErrCorrupt) && !errors.Is(e3, api.ErrParam)
	v1, ok1 := eng2.Get("a")
	check("三类错误+被拒后状态不变", distinct && ok1 && v1 == "1" && eng2.Put("b", "2") == nil)
	// 大 m：fanout=10000 不触发合并，层 0 段数 O(1) 可查（扫描上界由 tree 包测试钉住）。
	big, _ := tree.New(1, 10000, 1)
	for i := 0; i < 9999; i++ {
		_ = big.Put(fmt.Sprint("k", i), "v")
	}
	_, okBig := big.Get("k9998")
	check("大m段数可查不随m扫描", len(big.Segments()) == 9998 && okBig)
	// 并发：一个写 goroutine 反复触发合并，多个读 goroutine 读到的值必须属于已写集合。
	eng3, _ := api.New(2, 2, 1)
	done := make(chan struct{})
	var wg sync.WaitGroup
	var badRead atomic.Bool
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if v, ok := eng3.Get("a"); ok {
					n, err := strconv.Atoi(v)
					badRead.Store(badRead.Load() || err != nil || n < 0 || n >= 2000)
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		_ = eng3.Put("a", strconv.Itoa(i))
	}
	close(done)
	wg.Wait()
	last, _ := eng3.Get("a")
	check("并发读值合法+终值正确", !badRead.Load() && last == "1999")

	check("SelfCheck", eng.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
