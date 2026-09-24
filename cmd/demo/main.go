package main

import (
	"fmt"
	"strings"

	"ontology/logical"
	"ontology/props"
)

func main() {
	checks := []bool{
		join("k=v\\\n   w") == "k=vw" && !scanned("k=v\\\n   w").Comment,
		join("k=a\\\n   ") == "k=a",
		match(map[string]string{
			"key": "value",
		}, "key=value\nkey:value\nkey value\nkey = value  \nk==v\n=v\nonly\n"),
		!has("# c\n! c\nk=v # x\n") || same(map[string]string{"k": "v # x"}, "# c\n! c\nk=v # x\n"),
	}

	names := []string{"continuation", "blank continuation", "separators", "comments"}
	for i, ok := range checks {
		fmt.Printf("%s: %s\n", names[i], result(ok))
	}
	fmt.Printf("total: %d/%d\n", count(checks), len(checks))
	if count(checks) != len(checks) {
		panic("demo failed")
	}
}

func scanned(input string) *logical.Line {
	scanner, err := logical.NewScanner(strings.NewReader(input))
	if err != nil {
		panic(err)
	}
	line, _ := scanner.Next()
	return line
}

func join(input string) string {
	var builder strings.Builder
	for _, part := range scanned(input).Parts {
		builder.Write(part.Data)
	}
	return builder.String()
}

func loaded(input string) *props.Properties {
	result, err := props.Load(strings.NewReader(input))
	if err != nil {
		panic(err)
	}
	return result
}

func get(input, key string) string {
	value, _ := loaded(input).Get(key)
	return value
}

func match(want map[string]string, input string) bool {
	result := loaded(input)
	if result.Len() != len(want) {
		return false
	}
	for key, value := range want {
		got, ok := result.Get(key)
		if !ok || got != value {
			return false
		}
	}
	return true
}

func same(want map[string]string, input string) bool {
	return match(want, input)
}

func has(input string) bool {
	return loaded(input).Len() > 0
}

func result(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

func count(checks []bool) int {
	passed := 0
	for _, ok := range checks {
		if ok {
			passed++
		}
	}
	return passed
}
