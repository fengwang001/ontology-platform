package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"ontology/logical"
	"ontology/props"
)

type result struct {
	name string
	ok   bool
}

func main() {
	checks := []result{
		{name: "delimiters-and-spacing", ok: testDelimiters()},
		{name: "comments", ok: testComments()},
		{name: "continuations", ok: testContinuations()},
		{name: "escapes-and-unicode-error", ok: testEscapes()},
		{name: "duplicate-order", ok: testDuplicateOrder()},
		{name: "store-special-characters", ok: testStoreSpecial()},
		{name: "random-roundtrip-1000", ok: testRandomRoundtrip()},
		{name: "logical-check-counter", ok: testCounter()},
	}
	passed := 0
	for _, item := range checks {
		if item.ok {
			passed++
			fmt.Printf("OK %s\n", item.name)
			continue
		}
		fmt.Printf("FAIL %s\n", item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
}

func load(input string) (map[string]string, []string, error) {
	loaded, err := props.Load(input)
	if err != nil {
		return nil, nil, err
	}
	pairs := loaded.All()
	items, order := map[string]string{}, make([]string, 0, len(pairs))
	for _, pair := range pairs {
		items[pair.Key] = pair.Value
		order = append(order, pair.Key)
	}
	return items, order, nil
}

func same(items map[string]string, order []string, want map[string]string) bool {
	if len(items) != len(want) {
		return false
	}
	index := 0
	for key, value := range want {
		if order[index] != key || items[key] != value {
			return false
		}
		index++
	}
	return true
}

func testDelimiters() bool {
	input := "key=value\nkey2:value2\nkey3 value3\nkey4 = value  \nk==v\n=v\nk\n"
	items, order, err := load(input)
	want := map[string]string{"key": "value", "key2": "value2", "key3": "value3", "key4": "value  ", "k": "=v", "": "v"}
	return err == nil && same(items, order, want)
}

func testComments() bool {
	items, _, err := load("  # ignored\n! ignored\nk=v # x\n")
	return err == nil && len(items) == 1 && items["k"] == "v # x"
}

func testContinuations() bool {
	cases := []string{
		"k=v\\\n   w\n",
		"k=v\\\\\nx=y\n",
		"k=v\\\\\\\n  w\n",
		"k=a\\\n#b\n",
		"k=v\\",
		"k=v\\\n   \nz=y\n",
	}
	wants := []map[string]string{
		{"k": "vw"}, {"k": `v\`, "x": "y"}, {"k": `v\w`},
		{"k": "a#b"}, {"k": "v"}, {"k": "v", "z": "y"},
	}
	for index, input := range cases {
		items, _, err := load(input)
		if err != nil || !same(items, orderedKeys(wants[index]), wants[index]) {
			return false
		}
	}
	return true
}

func orderedKeys(items map[string]string) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	return keys
}

func testEscapes() bool {
	items, _, err := load("t=a\\tb\\nc\\rd\\fe\na\\=b=c\nk\\ 2=v\n")
	if err != nil || items["t"] != "a\tb\nc\rd\fe" || items["a=b"] != "c" || items["k 2"] != "v" {
		return false
	}
	_, err = props.Load("k=\\u12xy\n")
	var unicodeError *props.UnicodeError
	return errors.As(err, &unicodeError) && unicodeError.Line == 1 && unicodeError.Column == 3
}

func testDuplicateOrder() bool {
	items, order, err := load("a=1\nb=2\na=3\n")
	return err == nil && items["a"] == "3" && strings.Join(order, ",") == "a,b"
}

func testStoreSpecial() bool {
	pairs := []props.Pair{{"#!", "v"}, {" k=:a\\\t", " leading#! =: \t中"}, {"", ""}}
	text := props.New(pairs).Store()
	loaded, err := props.Load(text)
	return err == nil && equalPairs(loaded.All(), pairs)
}

func equalPairs(got, want []props.Pair) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func testRandomRoundtrip() bool {
	random := rand.New(rand.NewSource(162))
	alphabet := []rune("abc #!=:\\ \t\n\f中")
	pairs := make([]props.Pair, 1000)
	seen := map[string]bool{}
	for index := range pairs {
		key := randomString(random, alphabet, seen)
		pairs[index] = props.Pair{Key: key, Value: randomString(random, alphabet, nil)}
	}
	loaded, err := props.Load(props.New(pairs).Store())
	return err == nil && equalPairs(loaded.All(), pairs)
}

func randomString(random *rand.Rand, alphabet []rune, seen map[string]bool) string {
	for {
		runes := make([]rune, random.Intn(8))
		for index := range runes {
			runes[index] = alphabet[random.Intn(len(alphabet))]
		}
		text := string(runes)
		if seen == nil {
			return text
		}
		if !seen[text] {
			seen[text] = true
			return text
		}
	}
}

func testCounter() bool {
	var builder strings.Builder
	for builder.Len() < 1<<20 {
		builder.WriteString("k")
		builder.WriteString(strings.Repeat("a", 4095))
		builder.WriteString("\\\n  ")
	}
	scanner := logical.NewScanner(builder.String())
	scanner.Scan()
	return scanner.Checks() <= 2*len(builder.String())
}
