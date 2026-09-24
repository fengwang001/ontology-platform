// demo：CDC schema 演进列映射的端到端判定。退出码 0 表示全部通过。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/sch"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func sixVer() *api.Service {
	s := api.New(
		api.Column{Name: "x", Typ: sch.Int},
		api.Column{Name: "y", Typ: sch.Str},
		api.Column{Name: "m", Typ: sch.Str},
	)
	s.AddColumn("z", "str", true)
	s.ChangeType("y", "int")
	s.ChangeType("x", "str")
	s.DropColumn("m")
	s.AddColumn("w", "int", false)
	return s
}

func main() {
	s := sixVer()
	// 1. 第三节六个事件：逐列映射或拒收
	ev := func(v int, vals ...any) ([]any, error) { return s.Map(v, vals) }
	g1, e1 := ev(6, "1", int64(2), "a", int64(3))
	g2, e2 := ev(2, int64(5), "6", "M", "b")
	_, e3 := ev(2, int64(5), "abc", "M", "b")
	g4, e4 := ev(4, "9", int64(10), "M", "d")
	_, e5 := ev(1, int64(11), "12", "M")
	g6, e6 := ev(3, int64(13), int64(14), "M", "e")
	check("六事件", e1 == nil && reflect.DeepEqual(g1, []any{"1", int64(2), "a", int64(3)}) &&
		e2 == nil && reflect.DeepEqual(g2, []any{"5", int64(6), "b", int64(0)}) && // y=6
		errors.Is(e3, sch.ErrBadValue) &&
		e4 == nil && reflect.DeepEqual(g4, []any{"9", int64(10), "d", int64(0)}) && // w=0
		errors.Is(e5, sch.ErrMissingColumn) &&
		e6 == nil && reflect.DeepEqual(g6, []any{"13", int64(14), "e", int64(0)}))

	// 2. 四类可判定错误互不相同
	errs := []error{
		s.AddColumn("q", "float", false), s.AddColumn("", "int", false),
		s.AddColumn("x", "int", false), s.ChangeType("no", "int"), s.DropColumn("no"),
	}
	_, verr := s.Map(99, nil)
	distinct := verr != nil && errors.Is(verr, sch.ErrVersionUnregistered)
	for i := range errs {
		distinct = distinct && errs[i] != nil
		for j := i + 1; j < len(errs); j++ {
			distinct = distinct && !errors.Is(errs[i], errs[j])
		}
	}
	check("四类错误互异", distinct)

	// 3. 被拒后状态不变且可继续用
	before := fmt.Sprint(s.Active())
	s.AddColumn("bad", "float", false)
	s.DropColumn("ghost")
	_, e := s.Map(2, []any{int64(5), "abc", "M", "b"})
	g, eOK := s.Map(6, []any{"1", int64(2), "a", int64(3)})
	check("拒绝不留痕", fmt.Sprint(s.Active()) == before && errors.Is(e, sch.ErrBadValue) &&
		eOK == nil && len(g) == 4)

	// 4. 大 m：10000 列事件映射正确（单列 O(1) 定位由 mapc 内部测试钉住）
	big := api.New()
	for i := 0; i < 10000; i++ {
		big.AddColumn(fmt.Sprintf("c%d", i), "int", false)
	}
	vals := make([]any, 10000)
	for i := range vals {
		vals[i] = int64(i)
	}
	bg, be := big.Map(10000, vals) // 含全部 m 列的事件即最新版本事件
	check("大m映射", be == nil && len(bg) == 10000 && bg[9999] == int64(9999))

	// 5. 并发 Map 结果一致（严格对应某一完整版本）
	c := api.New(api.Column{Name: "a", Typ: sch.Int})
	done := make(chan struct{})
	var wg sync.WaitGroup
	mixed := false
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.AddColumn(fmt.Sprintf("c%d", i), []string{"int", "str"}[i%2], false)
		}
		close(done)
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				got, err := c.Map(1, []any{int64(7)})
				if err != nil || got[0] != int64(7) {
					mixed = true
					return
				}
				for j := 1; j < len(got); j++ {
					want := any(int64(0))
					if (j-1)%2 == 1 {
						want = any("")
					}
					if got[j] != want {
						mixed = true
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	check("并发一致", !mixed)

	// 6. 自检
	check("SelfCheck", s.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
