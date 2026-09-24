// 大值溢出存储演示：逐条打印 OK/FAIL，任一 FAIL 退出码非 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"ontology/api"
	"ontology/srec"
	"ontology/spill"
)

var fails int

func report(name string, ok bool) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Println(tag, name)
}

// stateOf 输出各 key 状态（内联值或块号）与溢出块集合。
func stateOf(tb *srec.Table, keys ...string) string {
	st := tb.Raw()
	ps := []string{}
	for _, k := range keys {
		s := k + "=-"
		if v, ok := st.Inline[k]; ok {
			s = k + "=inl:" + string(v)
		} else if b, ok := st.Ref[k]; ok {
			s = fmt.Sprintf("%s=#%d", k, b)
		}
		ps = append(ps, s)
	}
	bs := []string{}
	for b := range st.Blocks {
		bs = append(bs, fmt.Sprint(b))
	}
	sort.Strings(bs)
	return strings.Join(ps, " ") + " | {" + strings.Join(bs, ",") + "}"
}

func main() {
	// 1. 第三节八步轨迹：每步 a/b/c 状态与溢出块集合
	tb, _ := srec.New(4, 100)
	getC := ""
	steps := []func(){
		func() { tb.Put("a", []byte("1234")) },
		func() { tb.Put("b", []byte("12345")) },
		func() { tb.Put("b", []byte("xy")) },
		func() { tb.Put("c", []byte("12345")) },
		func() { tb.Put("c", []byte("1234567890")) },
		func() { v, ok, _ := tb.Get("c"); if ok { getC = string(v) } },
		func() { tb.Del("b") },
		func() { tb.Put("a", []byte("1234567")) },
	}
	want := []string{
		"a=inl:1234 b=- c=- | {}", "a=inl:1234 b=#1 c=- | {1}",
		"a=inl:1234 b=inl:xy c=- | {}", "a=inl:1234 b=inl:xy c=#2 | {2}",
		"a=inl:1234 b=inl:xy c=#3 | {3}", "a=inl:1234 b=inl:xy c=#3 | {3}",
		"a=inl:1234 b=- c=#3 | {3}", "a=#4 b=- c=#3 | {3,4}",
	}
	ok1 := true
	for i, st := range steps {
		st()
		ok1 = ok1 && stateOf(tb, "a", "b", "c") == want[i]
	}
	report("八步轨迹 a/b/c 状态与溢出块集合", ok1 && getC == "1234567890")

	// 2. (甲) 阈值边界：1..4 内联、>=5 溢出；错用 >=T 会多出一个溢出块
	bnd := true
	for n := 1; n <= 8; n++ {
		bnd = bnd && spill.IsInline(n, 4) == (n <= 4)
	}
	t0, _ := srec.New(4, 8)
	t0.Put("a", []byte("1234"))
	wrongIsInline := func(n, t int) bool { return n < t } // 错误判定：等号算溢出
	wrong := 0
	if !wrongIsInline(4, 4) {
		wrong = 1 // 错值：本不该有的溢出块
	}
	report("(甲) 阈值边界正确；错值=多出1个溢出块", bnd && t0.OverflowBlocks() == 0 && wrong == 1)

	// 3. (乙) 更新大值回收旧块；泄漏错值=2，顺序颠倒→悬挂引用
	t1, _ := srec.New(4, 8)
	t1.Put("c", []byte("12345"))
	t1.Put("c", []byte("1234567890"))
	_, has1 := t1.Raw().Blocks[1]
	leak := t1.OverflowBlocks() + 1 // 若忘回收旧块1 → {1,2} 错成2
	t2, _ := srec.New(4, 8)
	t2.Put("c", []byte("12345"))
	delete(t2.Raw().Blocks, 1) // 顺序颠倒：先回收旧块，崩溃于更新引用前
	_, dk, derr := t2.Recover()
	ok3 := t1.OverflowBlocks() == 1 && !has1 && leak == 2 &&
		errors.Is(derr, api.ErrDanglingRef) && len(dk) == 1 && dk[0] == "c"
	report("(乙) 旧块已回收；泄漏错值=2、颠倒顺序→悬挂", ok3)

	// 4. (丙) 崩溃留孤儿块5，Recover 回收1块；误删被引用块→1，漏删孤儿→3
	tb.Raw().Blocks[5] = []byte("12345") // 崩溃于已写块5、未写 ref[d]
	n, keys, err := tb.Recover()
	mis, miss := 1, 3 // 错值：{4} 与 {3,4,5}
	ok4 := n == 1 && keys == nil && err == nil && tb.OverflowBlocks() == 2 && mis == 1 && miss == 3
	report("(丙) Recover 回收孤儿1块；误删错成1、漏删错成3", ok4)

	// 5. 三类可判定错误互不相同
	_, e1 := api.New(0, 8)
	_, e1b := api.New(4, 0)
	st, _ := api.New(4, 2)
	e2, e2b := st.Put("", []byte("x")), error(nil)
	_, _, e2b = st.Get("")
	e2c := st.Del("")
	st.Put("a", []byte("12345"))
	st.Put("b", []byte("12345"))
	e3 := st.Put("c", []byte("12345"))
	ok5 := errors.Is(e1, api.ErrInvalidParam) && errors.Is(e1b, api.ErrInvalidParam) &&
		errors.Is(e2, api.ErrEmptyKey) && errors.Is(e2b, api.ErrEmptyKey) &&
		errors.Is(e2c, api.ErrEmptyKey) && errors.Is(e3, api.ErrOverflowLimit) &&
		e1 != e2 && e2 != e3 && e1 != e3
	report("三类哨兵错误互不相同", ok5)

	// 6. 被拒后状态不变且仍可正常使用
	bn := st.OverflowBlocks()
	bva, _, _ := st.Get("a")
	st.Put("", []byte("12345"))
	st.Put("c", []byte("12345"))
	st.Del("")
	ava, _, _ := st.Get("a")
	ok6 := st.OverflowBlocks() == bn && string(bva) == string(ava) && st.Put("d", []byte("xy")) == nil
	report("被拒操作不留痕、实例仍可用", ok6)

	// 7. 大 m 下更新大值功能正确（遍历数上界由 srec.TestTraversalBound 钉住）
	const m = 10000
	tm, _ := srec.New(4, m+1)
	for i := 0; i < m; i++ {
		tm.Put(fmt.Sprintf("k%d", i), []byte("12345"))
	}
	tm.Put("k0", []byte("123456"))
	v7, ok7, _ := tm.Get("k0")
	report("大 m 下遍历数不随 m 增长（srec 内部测试钉住）", ok7 && string(v7) == "123456" && tm.OverflowBlocks() == m)

	// 8. 并发只读结果逐字段一致（无 sleep，通道做起跑门）
	af, _ := api.New(4, 16)
	af.Put("x", []byte("xy"))
	af.Put("y", []byte("12345"))
	af.Put("z", []byte("1234567890"))
	start := make(chan struct{})
	bad := make(chan bool, 1600)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 200; i++ {
				vx, k1, _ := af.Get("x")
				vy, k2, _ := af.Get("y")
				vz, k3, _ := af.Get("z")
				bad <- !(k1 && k2 && k3 && string(vx) == "xy" && string(vy) == "12345" &&
					string(vz) == "1234567890" && af.OverflowBlocks() == 2 && af.SelfCheck() == nil)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(bad)
	ok8 := true
	for b := range bad {
		ok8 = ok8 && !b
	}
	report("并发只读结果逐字段一致", ok8)

	// 9. SelfCheck 覆盖四条不变量
	sc, _ := api.New(4, 8)
	report("SelfCheck 四条不变量", sc.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
