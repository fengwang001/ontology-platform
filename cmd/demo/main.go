package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/lex"
	"ontology/words"
)

type check struct {
	name string
	ok   bool
}

func wordsOK(input string, want []string) bool {
	got, err := words.Split(input)
	return err == nil && reflect.DeepEqual(got, want)
}

func errorOffset(input string, sentinel error, want int) bool {
	_, err := words.Split(input)
	var syntax *words.SyntaxError
	return errors.As(err, &syntax) && errors.Is(err, sentinel) && syntax.Offset == want
}

func allChunksOK(input string) bool {
	want, err := words.Split(input)
	if err != nil {
		return false
	}
	for cut := 0; cut <= len(input); cut++ {
		splitter := words.NewSplitter()
		if err := splitter.Feed(input[:cut]); err != nil {
			return false
		}
		if err := splitter.Feed(input[cut:]); err != nil {
			return false
		}
		if err := splitter.Close(); err != nil || !reflect.DeepEqual(splitter.Words(), want) {
			return false
		}
	}
	return true
}

func main() {
	machine := &lex.Machine{}
	input := "a 'b' \"c\\\\d\""
	for index := range []byte(input) {
		machine.Step(input[index], index)
	}
	roundTrip := []string{"", "a b", "'", "\"", "\\", "$x", "a\nb", "中文"}

	checks := []check{
		{name: "单引号内反斜杠", ok: wordsOK(`'a\b' 'a\'`, []string{`a\b`, `a\`})},
		{name: "双引号五种转义", ok: wordsOK("\"a\\\"b\" \"a\\\\b\" \"a\\b\" \"a\\$\" \"a\\\nb\"", []string{`a"b`, `a\b`, `a\b`, `a$`, "ab"})},
		{name: "无引号转义续行", ok: wordsOK("a\\ b a\\\\ x\\\ny", []string{"a b", `a\`, "xy"})},
		{name: "拼接与空词", ok: wordsOK(`a"b"'c' a '' b ""'' a""`, []string{"abc", "a", "", "b", "", "a"})},
		{name: "三类错误偏移", ok: errorOffset(`'a`, words.ErrUnterminatedSingle, 0) && errorOffset(` "a\$`, words.ErrUnterminatedDouble, 1) && errorOffset(`ab\`, words.ErrDanglingBackslash, 2)},
		{name: "所有切分点", ok: allChunksOK("a\\ \"b\"'c' ''\"x\\\ny\"")},
		{name: "往返", ok: wordsOK(words.Quote(roundTrip), roundTrip)},
		{name: "计数器", ok: machine.Processed() == len(input)},
	}
	passed := 0
	for _, item := range checks {
		result := "OK"
		if item.ok {
			passed++
		} else {
			result = "FAIL"
		}
		fmt.Printf("%s %s\n", result, item.name)
	}
	fmt.Printf("总计 %d/%d\n", passed, len(checks))
}
