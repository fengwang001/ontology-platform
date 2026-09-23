// demo 演示倒排索引的短语查询与增量合并，逐条打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"ontology/merge"
	"ontology/phrase"
	"ontology/posting"
	"ontology/segment"
	"ontology/verify"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
	} else {
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
}

func listsOf(tokens []string) map[string]posting.List {
	bs := map[string]*posting.Builder{}
	for i, tok := range tokens {
		b, ok := bs[tok]
		if !ok {
			b = &posting.Builder{}
			bs[tok] = b
		}
		_ = b.Add(0, uint32(i))
	}
	out := map[string]posting.List{}
	for t, b := range bs {
		out[t] = b.List()
	}
	return out
}

func phraseCount(tokens []string, words ...string) int {
	lists := listsOf(tokens)
	var qs []posting.List
	for _, w := range words {
		qs = append(qs, lists[w])
	}
	return len(phrase.Phrase(qs, nil))
}

func checkPhrases() {
	check(`A A A 查 "A A" = 2`, phraseCount([]string{"A", "A", "A"}, "A", "A") == 2)
	check(`A A A A 查 "A A" = 3`, phraseCount([]string{"A", "A", "A", "A"}, "A", "A") == 3)
	check(`A B A B A 查 "A B A" = 2`,
		phraseCount([]string{"A", "B", "A", "B", "A"}, "A", "B", "A") == 2)
}

func checkComplexity() {
	const n = 10000
	tokens := make([]string, 0, 2*n)
	for i := 0; i < n; i++ {
		tokens = append(tokens, "A", "B")
	}
	lists := listsOf(tokens)
	var st phrase.Stats
	phrase.Phrase([]posting.List{lists["A"], lists["B"]}, &st)
	check(fmt.Sprintf("1万A+1万B 比较 %d <= %d", st.Comparisons(), 4*2*n),
		st.Comparisons() <= 4*2*n)
	mk := func(step, count uint32) posting.List {
		var b posting.Builder
		for i := uint32(0); i < count; i++ {
			_ = b.Add(i*step, 0)
		}
		return b.List()
	}
	var ast phrase.Stats
	phrase.And([]posting.List{mk(6, 100), mk(2, 150), mk(3, 200)}, &ast)
	check(fmt.Sprintf("AND 最短链驱动 比较 %d <= %d", ast.Comparisons(), 4*100*3),
		ast.Comparisons() <= 4*100*3)
}

func bigSegment() *segment.Segment {
	lists := map[string]posting.List{}
	for i := 0; i < 200; i++ {
		var b posting.Builder
		for d := uint32(0); d < 5; d++ {
			_ = b.Add(d, uint32(i%7))
			_ = b.Add(d, uint32(i%7)+10)
		}
		term := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+i/52))
		lists[term] = b.List()
	}
	return segment.New(lists)
}

func checkTruncation(dir string) {
	full := filepath.Join(dir, "full.seg")
	if err := segment.Write(full, bigSegment()); err != nil {
		check("写段文件", false)
		return
	}
	data, _ := os.ReadFile(full)
	cutAt := func(n int) string {
		p := filepath.Join(dir, "cut.seg")
		_ = os.WriteFile(p, data[:n], 0o644)
		return p
	}
	classes := []struct {
		name string
		cut  int
		want error
	}{
		{"头部不完整", 5, segment.ErrHeaderIncomplete},
		{"词典不完整", 20, segment.ErrDictIncomplete},
		{"倒排链不完整", len(data) - 10, segment.ErrPostingsIncomplete},
		{"CRC 不匹配", len(data) - 2, segment.ErrCRCMismatch},
	}
	for _, c := range classes {
		err := verify.CheckFile(cutAt(c.cut))
		check(fmt.Sprintf("截断分类: %s", c.name), errors.Is(err, c.want))
	}
	rec, err := segment.ReadRecover(cutAt(len(data) / 2))
	ok := errors.Is(err, segment.ErrPostingsIncomplete) && rec != nil &&
		len(rec.Terms) > 0 && len(rec.Terms) < 200 &&
		verify.CheckInvariants(rec) == nil
	check("截断恢复后词典无悬挂指针", ok)
}

func checkDeleteConsistency() {
	c := merge.New("")
	c.AddDoc([]string{"a", "b", "a"})
	c.AddDoc([]string{"b", "c"})
	c.AddDoc([]string{"a", "c"})
	c.Delete(2)
	before := c.Phrase("a")
	if err := c.Compact(); err != nil {
		check("删除一致性", false)
		return
	}
	check("删除后立即查询 == 合并后查询", reflect.DeepEqual(before, c.Phrase("a")))
}

func main() {
	dir, err := os.MkdirTemp("", "demo-seg")
	if err != nil {
		fmt.Println("FAIL 创建临时目录")
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	checkPhrases()
	checkComplexity()
	checkTruncation(dir)
	checkDeleteConsistency()
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
