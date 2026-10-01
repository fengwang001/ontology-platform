package ontology

import (
	"fmt"
	"reflect"
	"testing"
	"unicode/utf8"
)

func addWords(t *testing.T, f *Finder, words ...string) {
	t.Helper()
	for _, word := range words {
		if err := f.Add(word); err != nil {
			t.Fatalf("Add(%q): %v", word, err)
		}
	}
}

func assertMatches(t *testing.T, got []Match, want []Match, input string, basis string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		got = []Match{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s\ngot  %#v\nwant %#v\n判定依据: %s", input, got, want, basis)
	}
	t.Logf("输入: %s\n输出: %#v\n判定依据: %s", input, got, basis)
}

func totalCount(grams trigramSet) int {
	total := 0
	for _, count := range grams {
		total += count
	}
	return total
}

func TestTrigramCountingRules(t *testing.T) {
	grams := trigrams("a")
	if totalCount(grams) != 3 || len(grams) != 3 {
		t.Fatalf("单字符三元组总次数 = %d, distinct=%d, want both 3: %#v", totalCount(grams), len(grams), grams)
	}

	grams = trigrams("aaaa")
	want := trigramSet{"\x02\x02a": 1, "\x02aa": 1, "aaa": 2, "aa\x03": 1, "a\x03\x03": 1}
	if !reflect.DeepEqual(grams, want) {
		t.Fatalf("aaaa 多重集合 = %#v, want %#v", grams, want)
	}

	query := trigrams("aaaa")
	multisetWord := trigrams("aaaaa")
	setLikeWord := trigramSet{}
	for gram := range multisetWord {
		setLikeWord[gram] = 1
	}
	if got := intersectionCount(query, multisetWord); got != 6 {
		t.Fatalf("重复三元组按次数交 = %d, want 6", got)
	}
	if got := intersectionCount(query, setLikeWord); got != 5 {
		t.Fatalf("去重集合交 = %d, want 5", got)
	}
}

func TestSimilarRulesAndExamples(t *testing.T) {
	exampleWant := []Match{
		{Word: "abcde", Score: "1/1", Folded: 1},
		{Word: "abcx", Score: "6/13", Folded: 0},
	}

	f := NewFinder()
	addWords(t, f, "abcde", "abcd", "abcx")
	for _, k := range []int{2, 3} {
		got, err := f.Similar("abcde", 30, k)
		if err != nil {
			t.Fatal(err)
		}
		assertMatches(t, got, exampleWant, fmt.Sprintf("Similar(abcde,30,%d)", k), "I=7,4,3；abcd 折叠到 abcde；abcx 只比较已保留的 abcde")
	}
	got, err := f.Similar("abcde", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, exampleWant[:1], "Similar(abcde,30,1)", "截断只影响返回项，Folded 仍统计全部折叠")

	f = NewFinder()
	addWords(t, f, "abd")
	got, err = f.Similar("abc", 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{Word: "abd", Score: "2/5", Folded: 0}}, "Similar(abc,40,10)", "200*2 == 40*(5+5)，Dice=4/10 既约为 2/5")
	got, err = f.Similar("abc", 41, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{}, "Similar(abc,41,10)", "200*2 < 41*10，差一不命中")

	f = NewFinder()
	addWords(t, f, "ab", "abc")
	got, err = f.Similar("ab", 80, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{Word: "ab", Score: "1/1", Folded: 0}}, "Similar(ab,80,10)", "q 字符数 2，有效阈值 100，只有相同词命中")
	got, err = f.Similar("ab", 90, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{Word: "ab", Score: "1/1", Folded: 0}}, "Similar(ab,90,10)", "theta+20 超过 100 时封顶为 100")

	f = NewFinder()
	addWords(t, f, "éa", "éb")
	if runes := utf8.RuneCountInString("éa"); runes != 2 {
		t.Fatalf("éa rune 数 = %d, want 2", runes)
	}
	got, err = f.Similar("éa", 80, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{Word: "éa", Score: "1/1", Folded: 0}}, "Similar(éa,80,10)", "多字节字符按 rune 计长度，短查询有效阈值封顶 100")

	f = NewFinder()
	addWords(t, f, "abe", "abd")
	got, err = f.Similar("abc", 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{
		{Word: "abd", Score: "2/5", Folded: 0},
		{Word: "abe", Score: "2/5", Folded: 0},
	}, "Similar(abc,40,10)", "Dice 相等时按词项字节序升序")

	f = NewFinder()
	addWords(t, f, "abcdefghij", "abcXefghij", "abc")
	got, err = f.Similar("abcdefghij", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{
		{Word: "abcdefghij", Score: "1/1", Folded: 1},
		{Word: "abcXefghij", Score: "3/4", Folded: 0},
	}, "Similar(abcdefghij,30,10)", "abc 是两个已保留词的真前缀，归入序列中最先保留的精确命中")

	f = NewFinder()
	addWords(t, f, "abc", "abd")
	got, err = f.Similar("abc", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("theta=1 got %#v, want 2 hits", got)
	}
	got, err = f.Similar("abc", 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{Word: "abc", Score: "1/1", Folded: 0}}, "Similar(abc,100,10)", "theta=100 时只有相同词命中")
	got, err = f.Similar("xyz", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{}, "Similar(xyz,1,10)", "有词典但无命中时返回空结果")

	empty := NewFinder()
	got, err = empty.Similar("abc", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{}, "empty.Similar(abc,1,10)", "空词典返回空结果而不是错误")

	f = NewFinder()
	addWords(t, f, "abc")
	got, err = f.Similar("abc", 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, got, []Match{{Word: "abc", Score: "1/1", Folded: 0}}, "Similar(abc,100,10)", "k 大于命中数时返回实际数量")
}
