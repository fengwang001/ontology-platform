package main

import (
	"fmt"

	"ontology/patch"
	"ontology/udiff"
)

func main() {
	cases := [][2]string{
		{"x\n", "y\nx\n"},
		{"a\nb\nc\n", "a\nb\nX\nc\n"},
		{"a\n", ""},
		{"ab\r\ncd\r\n", "ab\r\nxy\r\ncd\r\n"},
		{"a\nb\n", "a\nb"},
		{"a\nb", "a\nb\n"},
		{"abc", "xyz"},
		{"", "x\n"},
		{"a\nb\nc\nd\ne\n", "a\nX\nc\nd\ne\n"},
	}
	for _, cs := range cases {
		p, err := udiff.Render([]byte(cs[0]), []byte(cs[1]), 3)
		if err != nil {
			panic(err)
		}
		fmt.Printf("==== %q -> %q\n%s", cs[0], cs[1], string(p))
		got, err := patch.Apply([]byte(cs[0]), p, patch.Options{Fuzz: 3})
		fmt.Printf("apply err=%v eq=%v\n", err, string(got) == cs[1])
		rev, err := patch.Reverse([]byte(cs[1]), p, patch.Options{Fuzz: 3})
		fmt.Printf("reverse err=%v eq=%v\n", err, string(rev) == cs[0])
	}
}
