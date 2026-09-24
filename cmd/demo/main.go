// Command demo 逐项演示大小写不敏感查找表的判定，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"ontology/api"
	"ontology/fold"
	"ontology/keymap"
)

var fails int

func ok(name string, cond bool) {
	mark := "OK  "
	if !cond {
		mark = "FAIL"
		fails++
	}
	fmt.Println(mark, name)
}

func main() {
	idem := true
	for _, s := range []string{"STRASSE", "straße", "ß", "İ", "Hello, 世界"} {
		idem = idem && fold.String(fold.String(s)) == fold.String(s)
	}
	ok("fold idempotent (incl. ß, İ)", idem)

	var sb strings.Builder
	for _, s := range []string{"STRASSE", "strasse", "straße", "İ"} {
		fmt.Fprintf(&sb, " %s→%s", s, fold.String(s))
	}
	fmt.Println("OK   folds:", strings.TrimSpace(sb.String()))

	km := keymap.New(16, 8)
	_ = km.Put("GoLang", "v1")
	v1, h1 := km.Get("golang")
	v2, h2 := km.Get("GOLANG")
	_, h3 := km.Get("rust")
	ok("case-insensitive hit, distinct miss", h1 && h2 && v1 == "v1" && v2 == "v1" && !h3)

	tb := api.New(16, 8)
	_ = tb.Put("GoLang", "v1")
	_ = tb.Put("golang", "v2")
	ok("original key kept on override", tb.Keys()[0] == "GoLang")

	a, b := api.New(16, 8), api.New(16, 8)
	for _, k := range []string{"Banana", "apple", "CHERRY"} {
		_ = a.Put(k, "x")
	}
	for _, k := range []string{"CHERRY", "apple", "Banana"} {
		_ = b.Put(k, "x")
	}
	ok("key order deterministic", fmt.Sprint(a.Keys()) == fmt.Sprint(b.Keys()))

	e1, e2 := tb.Put("", "x"), tb.Put(strings.Repeat("k", 17), "x")
	small := api.New(16, 1)
	_ = small.Put("a", "1")
	e3 := small.Put("b", "2")
	ok("3 distinct sentinel errors", errors.Is(e1, api.ErrEmptyKey) &&
		errors.Is(e2, api.ErrKeyLong) && errors.Is(e3, api.ErrFull) &&
		e1 != e2 && e2 != e3 && e1 != e3)
	ok("self-check after rejections", tb.SelfCheck() == nil && small.SelfCheck() == nil)

	single := true
	for _, n := range []int{1000, 100000} {
		s := strings.Repeat("aé世", n) // ASCII + 变音符 + 多字节
		single = single && utf8.RuneCountInString(fold.String(s)) == 3*n
	}
	ok("rune count conserved (1000/100000)", single)

	want := tb.Keys()
	var wg sync.WaitGroup
	consistent := int32(1)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, hit := tb.Get("GOLANG")
			if !hit || v != "v2" || fmt.Sprint(tb.Keys()) != fmt.Sprint(want) || tb.SelfCheck() != nil {
				atomic.StoreInt32(&consistent, 0)
			}
		}()
	}
	wg.Wait()
	ok("concurrent reads consistent", atomic.LoadInt32(&consistent) == 1)

	if fails > 0 {
		os.Exit(1)
	}
}
