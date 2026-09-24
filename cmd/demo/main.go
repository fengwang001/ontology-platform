// Command demo 对列式变更日志增量字典编码做端到端自检。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/dict"
	"ontology/enc"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// dict 层：code 按首次出现顺序分配，边界重置后回到 0。
	d := dict.New(4)
	alloc := d.Add("a") == 0 && d.Add("b") == 1 && d.Full() == false
	d.Add("c")
	d.Add("d")
	resetOK := alloc && d.Full()
	d.Reset()
	resetOK = resetOK && d.Len() == 0 && d.Add("e") == 0
	check("dict: code 按序分配且边界重置后回到 0", resetOK)

	// 大 m 功能代理：各档规模下新值仍是单次插入；探测数恒为 1 的严格
	// O(1) 断言由同包白盒测试 TestProbeCountO1 钉住（probes 非导出）。
	proxy := true
	for _, m := range []int{100, 1000, 10000} {
		dm := dict.New(m + 1)
		for i := 0; i < m; i++ {
			dm.Add(fmt.Sprintf("v%05d", i))
		}
		_, ok := dm.Lookup("brand-new")
		proxy = proxy && dm.Full() == false && !ok && dm.Add("brand-new") == m
	}
	check("dict: 大 m 下新值探测不随 m 增长（O(1)，白盒测试钉住）", proxy)

	// enc 层：第三节八步，逐步比对发射的 token；末步流即最终流。
	e := enc.NewEncoder(4)
	vals := []string{"a", "a", "a", "b", "c", "d", "e", "a"}
	steps := [][]enc.Token{
		{enc.Put(0, "a")},
		{enc.Put(0, "a"), enc.Ref(0, 1)},
		{enc.Put(0, "a"), enc.Ref(0, 2)},
		{enc.Put(0, "a"), enc.Ref(0, 2), enc.Put(1, "b")},
		{enc.Put(0, "a"), enc.Ref(0, 2), enc.Put(1, "b"), enc.Put(2, "c")},
		{enc.Put(0, "a"), enc.Ref(0, 2), enc.Put(1, "b"), enc.Put(2, "c"), enc.Put(3, "d")},
		{enc.Put(0, "a"), enc.Ref(0, 2), enc.Put(1, "b"), enc.Put(2, "c"), enc.Put(3, "d"), enc.Reset(), enc.Put(0, "e")},
		{enc.Put(0, "a"), enc.Ref(0, 2), enc.Put(1, "b"), enc.Put(2, "c"), enc.Put(3, "d"), enc.Reset(), enc.Put(0, "e"), enc.Put(1, "a")},
	}
	stepOK := true
	for i, v := range vals {
		e.Append(v)
		stepOK = stepOK && reflect.DeepEqual(e.Tokens(), steps[i])
	}
	check(fmt.Sprintf("enc: 八步逐步 token 与字典一致；最终流 %v", e.Tokens()), stepOK)

	// 往返一致。
	got, err := enc.Decode(4, e.Tokens())
	check("enc: Decode 往返逐元素一致", err == nil && reflect.DeepEqual(got, vals))

	// RLE 无损：展开 ref(code,m) 后与不压缩逐值发射的期望流逐 token 相同。
	wantFlat := []enc.Token{
		enc.Put(0, "a"), enc.Ref(0, 1), enc.Ref(0, 1),
		enc.Put(1, "b"), enc.Put(2, "c"), enc.Put(3, "d"),
		enc.Reset(), enc.Put(0, "e"), enc.Put(1, "a"),
	}
	flat := []enc.Token{}
	for _, t := range e.Tokens() {
		if t.Kind == enc.KindRef {
			for i := 0; i < t.Count; i++ {
				flat = append(flat, enc.Ref(t.Code, 1))
			}
		} else {
			flat = append(flat, t)
		}
	}
	check("enc: RLE 展开与不压缩逐值发射逐 token 相同", reflect.DeepEqual(flat, wantFlat))

	// api 层：三类哨兵错误互不相同；被拒操作不留痕，之后仍可正常使用。
	c, _ := api.New(4)
	for _, v := range vals {
		c.Append(v)
	}
	snap := c.Tokens()
	_, eCfg := api.New(0)
	eVal := c.Append("")
	_, eTok := c.Decode([]enc.Token{enc.Ref(9, 1)})
	errOK := errors.Is(eCfg, api.ErrInvalidConfig) && errors.Is(eVal, api.ErrInvalidValue) &&
		errors.Is(eTok, api.ErrBadToken) && eCfg != eVal && eVal != eTok && eCfg != eTok
	errOK = errOK && reflect.DeepEqual(c.Tokens(), snap)
	errOK = errOK && c.Append("z") == nil && c.SelfCheck() == nil
	check("api: 三类错误互不相同、被拒不留痕且仍可用、SelfCheck 通过", errOK)

	// 并发：N 个 goroutine 并发 Decode 同一份流，并发 Tokens 取快照，全部一致。
	toks := c.Tokens()
	wantSeq := append(append([]string{}, vals...), "z")
	const n = 32
	var wg sync.WaitGroup
	concOK := true
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ds, derr := c.Decode(toks)
			ts := c.Tokens()
			mu.Lock()
			concOK = concOK && derr == nil && reflect.DeepEqual(ds, wantSeq) &&
				reflect.DeepEqual(ts, toks)
			mu.Unlock()
		}()
	}
	wg.Wait()
	check("api: 并发 Decode/Tokens 结果逐元素一致（-race）", concOK)

	if failed {
		os.Exit(1)
	}
}
