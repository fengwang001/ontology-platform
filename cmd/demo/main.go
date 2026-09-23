// demo 逐条演示 headers 包的语义，每步一行 OK/FAIL，最后一行总计。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/headers"
	"ontology/listval"
)

var failed int

func check(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
		failed++
	}
	fmt.Printf("%-4s %s\n", mark, name)
}

func main() {
	// 1. 保序与重复：同名不去重、不重排。
	s, err := headers.Parse([]byte("B: 1\r\nA: 2\r\nB: 3\r\n\r\n"), nil)
	check("保序与重复", err == nil && s.Len() == 3 &&
		slices.Equal(s.GetAll("B"), []string{"1", "3"}) &&
		string(s.Bytes()) == "B: 1\r\nA: 2\r\nB: 3\r\n\r\n")

	// 2. 四种单值策略：取首/取末/合并/报错。
	m, _ := headers.Parse([]byte("X-A: p\r\nX-A: q\r\nX-Last: 1\r\nX-Last: 2\r\n"+
		"X-Multi: a\r\nX-Multi: b\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\n"), nil)
	v1, e1 := m.Get("X-A")
	v2, _ := m.Get("X-Last")
	v3, _ := m.Get("X-Multi")
	_, e4 := m.Get("Content-Length")
	check("四种单值策略", e1 == nil && v1 == "p" && v2 == "2" && v3 == "a, b" &&
		errors.Is(e4, headers.ErrDuplicate))

	// 3. 折行往返无损：重折后再解析得到完全相同的值。
	long := "value with many words that certainly exceeds the folding width limit"
	f, _ := headers.Parse([]byte("X-Long: "+long+"\r\n\r\n"), &headers.Config{Width: 40})
	f2, err := headers.Parse(f.Bytes(), &headers.Config{Width: 40})
	check("折行往返", err == nil && f2.GetAll("X-Long")[0] == long)

	// 4. 首行续行判错。
	_, err = headers.Parse([]byte(" X: 1\r\n\r\n"), nil)
	check("首行续行判错", isParseErr(err, headers.StageFold))

	// 5. 名字与冒号间空白判错。
	_, err = headers.Parse([]byte("X-Name : 1\r\n\r\n"), nil)
	check("冒号前空白判错", isParseErr(err, headers.StageName))

	// 6. 注入变体全拒：CRLF/裸LF/裸CR/NUL/编码绕过，回写无额外头部。
	inj := headers.New(nil)
	variants := []string{"a\r\nEvil: 1", "a\nEvil: 1", "a\rEvil: 1", "a\x00Evil",
		"a%0d%0aEvil: 1", "a%0AEvil: 1", "a%00Evil"}
	rejected := 0
	for _, v := range variants {
		if err := inj.Add("X-Test", v); err != nil {
			rejected++
		}
	}
	out := inj.Bytes()
	check("注入变体全拒", rejected == len(variants) && inj.Len() == 0 &&
		!bytes.Contains(out, []byte("Evil")))

	// 7. 引号内逗号不切分，转义引号正确处理。
	items, err := listval.Split(`"a,b", "c\"d,e", f`)
	check("引号内逗号", err == nil && slices.Equal(items, []string{`"a,b"`, `"c\"d,e"`, "f"}))

	// 8. 列表往返语义等价：切分→合并→再切分得到相同项序列。
	src := `a, "x,y", b,,  c  `
	items1, _ := listval.Split(src)
	items2, _ := listval.Split(listval.Join(items1))
	check("列表往返", slices.Equal(items1, items2) && slices.Equal(items1, []string{"a", `"x,y"`, "b", "c"}))

	// 9. 回写幂等：不规范输入规范化后，再解析再回写字节相同。
	n1, err := headers.Parse([]byte("x-a:  1 \r\nX-B: 2\r\n\r\n"), nil)
	b1 := n1.Bytes()
	n2, err2 := headers.Parse(b1, nil)
	check("回写幂等", err == nil && err2 == nil && n1.Normalized() &&
		bytes.Equal(b1, n2.Bytes()))

	// 10. 遍历所有截断点：全部返回可判定错误，不 panic。
	sample := []byte("X-One: 1\r\nX-Two: longer value\r\n folded\r\n\r\n")
	truncOK := true
	for i := 0; i < len(sample); i++ {
		if _, err := headers.Parse(sample[:i], nil); err == nil {
			truncOK = false
		}
	}
	check("截断点遍历", truncOK)

	// 11. 四类超限：彼此可判定，拒绝后状态零变化。
	lim := headers.Limits{MaxEntries: 1, MaxNameLen: 4, MaxValueLen: 3, MaxBytes: 20}
	lcfg := &headers.Config{Limits: lim}
	l, _ := headers.Parse([]byte("Abc: ok\r\n\r\n"), lcfg)
	eEntries := l.Add("Def", "x")
	eName := l.Add("Too-Long-Name", "x")
	eValue := l.Add("Abc", "toolongvalue")
	_, eBytes := headers.Parse([]byte("X: 1234567890123456789012345\r\n\r\n"), lcfg)
	check("四类超限", errors.Is(eEntries, headers.ErrTooManyEntries) &&
		errors.Is(eName, headers.ErrNameTooLong) && errors.Is(eValue, headers.ErrValueTooLong) &&
		errors.Is(eBytes, headers.ErrTooLarge) && l.Len() == 1)

	// 12. 查找比较数两组对照：N=50 与 N=5000 比较数相同。
	cmp50 := lookups(50)
	cmp5000 := lookups(5000)
	check("查找比较数对照", cmp50 == cmp5000 && cmp50 <= 2)

	// 13. 并发查询与串行一致。
	check("并发一致", concurrentConsistent(m))

	fmt.Printf("总计: %d 项失败\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func isParseErr(err error, stage headers.Stage) bool {
	var pe *headers.ParseError
	return errors.As(err, &pe) && pe.Stage == stage
}

func lookups(n int) int64 {
	s := headers.New(&headers.Config{Width: 78}) // 零值 Limits：不限条数
	for i := 0; i < n; i++ {
		_ = s.Add(fmt.Sprintf("X-H%d", i), "v")
	}
	_ = s.Add("Target", "v")
	before := s.Compares()
	s.GetAll("Target")
	return s.Compares() - before
}

func concurrentConsistent(s *headers.Set) bool {
	want, _ := s.Get("X-Multi")
	var wg sync.WaitGroup
	var mismatch atomic.Bool
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				v, _ := s.Get("X-Multi")
				if v != want || !strings.Contains(v, "a") {
					mismatch.Store(true)
				}
				_ = s.Len()
				_ = s.Count("X-Last")
				_ = s.Bytes()
			}
		}()
	}
	wg.Wait()
	return !mismatch.Load()
}
