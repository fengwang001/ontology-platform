package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"sync"

	"ontology/api"
	"ontology/fww"
)

var fails int

func ok(cond bool, name string) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fails++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	// 第三节六步场景（经 fww 层）
	ws := []fww.Write{
		{Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"}, {Key: "K", Seq: 10, Val: "c"},
		{Key: "K", Seq: 1, Val: "d"}, {Key: "K", Seq: 5, Val: "e"}, {Key: "K", Seq: 8, Val: "f"},
	}
	wantLog := [][]fww.Change{
		{{Key: "K", Seq: 7, Val: "a"}},
		{{Retract: true, Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"}},
		{},
		{{Retract: true, Key: "K", Seq: 3, Val: "b"}, {Key: "K", Seq: 1, Val: "d"}},
		{},
		{},
	}
	wantVal := []string{"a", "b", "b", "d", "d", "d"}
	m := fww.New()
	stepsOK, shadow := true, map[string]fww.Change{}
	retractOK := true
	for i, w := range ws {
		log, err := m.Feed([]fww.Write{w})
		if err != nil || !slices.Equal(log, wantLog[i]) || m.View()["K"] != wantVal[i] {
			stepsOK = false
		}
		for _, c := range log { // 逐条应用，校验撤回恰好等于当前值
			if c.Retract {
				cur, ok := shadow[c.Key]
				if !ok || cur.Seq != c.Seq || cur.Val != c.Val {
					retractOK = false
				}
				delete(shadow, c.Key)
			} else {
				shadow[c.Key] = c
			}
		}
	}
	ok(stepsOK, "六步输出与生效值")
	ok(m.View()["K"] == "d" && m.Dropped() == 3, "最终生效值(1,d) 丢弃数3")
	ok(retractOK, "撤回恰好匹配当前值")

	// LWW 对照：保留最大 Seq，第 3 步 (10,c) 应输出 -(K,7,a) +(K,10,c)，终值错成 (10,c)
	lseq, lval, lset := int64(0), "", false
	var step3 []fww.Change
	for i, w := range ws {
		if !lset || w.Seq > lseq {
			if lset {
				if i == 2 {
					step3 = append(step3, fww.Change{Retract: true, Key: w.Key, Seq: lseq, Val: lval})
				}
			}
			if i == 2 {
				step3 = append(step3, fww.Change{Key: w.Key, Seq: w.Seq, Val: w.Val})
			}
			lseq, lval, lset = w.Seq, w.Val, true
		}
	}
	ok(slices.Equal(step3, []fww.Change{
		{Retract: true, Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 10, Val: "c"},
	}) && lseq == 10 && lval == "c", "LWW对照 第3步撤7加10 终值(10,c)")

	// api 层：三类可判定错误互不相同 + 被拒后状态不变
	reg := api.New()
	_, seedErr := reg.Feed([]api.Write{{Key: "K", Seq: 5, Val: "a"}})
	errs := []error{}
	for _, b := range [][]api.Write{
		{{Key: "", Seq: 1, Val: "x"}},
		{{Key: "K", Seq: 0, Val: "x"}},
		{{Key: "K", Seq: 5, Val: "x"}},
	} {
		_, err := reg.Feed(b)
		errs = append(errs, err)
	}
	errsOK := seedErr == nil && errors.Is(errs[0], api.ErrEmptyKey) &&
		errors.Is(errs[1], api.ErrBadSeq) && errors.Is(errs[2], api.ErrDupSeq) &&
		errs[0] != errs[1] && errs[1] != errs[2] && errs[0] != errs[2]
	ok(errsOK, "三类可判定错误互不相同")
	ok(reg.View()["K"] == "a" && len(reg.View()) == 1 && reg.Dropped() == 0, "被拒后状态不变")

	// 大 m 下检查个数不随 m 增长（反射读非导出计数器，公开接口不含它）
	bigOK := true
	for _, n := range []int{100, 1000, 10000} {
		fm := fww.New()
		batch := make([]fww.Write, n)
		for i := range batch {
			batch[i] = fww.Write{Key: fmt.Sprintf("k%d", i), Seq: 1, Val: "v"}
		}
		if _, err := fm.Feed(batch); err != nil {
			bigOK = false
		}
		if _, err := fm.Feed([]fww.Write{{Key: "k0", Seq: 2, Val: "w"}}); err != nil {
			bigOK = false
		}
		if reflect.ValueOf(fm).Elem().FieldByName("checked").Int() > 2 {
			bigOK = false
		}
	}
	ok(bigOK, "大m下检查个数为常数")

	// 并发只读：N 个 goroutine 视图逐字段相同
	full := api.New()
	_, _ = full.Feed(ws)
	base := full.View()
	var wg sync.WaitGroup
	concOK := true
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := full.View()
			if !maps.Equal(v, base) || full.Dropped() != 3 || full.SelfCheck() != nil {
				concOK = false
			}
		}()
	}
	wg.Wait()
	ok(concOK, "并发只读结果一致")
	ok(api.New().SelfCheck() == nil, "SelfCheck")

	if fails > 0 {
		os.Exit(1)
	}
}
