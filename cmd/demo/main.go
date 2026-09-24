package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"ontology/find"
	"ontology/scan"
	"ontology/table"
)

func main() {
	results := []bool{}
	t, err := table.New("ababaca")
	want := []int{0, 0, 1, 2, 3, 0, 1}
	ok := err == nil && t.Len() == len(want)
	for i, v := range want {
		ok = ok && t.At(i) == v
	}
	results = append(results, ok)
	scanner := scan.New(t)
	positions := scanner.Scan("abababaca")
	results = append(results, len(positions) == 1 && positions[0] == 2)

	m, err := find.Compile("aa", 8)
	overlap, _ := m.FindAll("aaaa")
	results = append(results, reflect.DeepEqual(overlap, []int{0, 1, 2}))
	text := make([]byte, 80)
	alphabet := []byte("abc")
	for i := range text {
		text[i] = alphabet[rand.IntN(len(alphabet))]
	}
	naive := []int{}
	for i := 0; i+2 <= len(text); i++ {
		if string(text[i:i+2]) == "ab" {
			naive = append(naive, i)
		}
	}
	random, _ := m.FindAll(string(text))
	results = append(results, reflect.DeepEqual(random, naive))

	m7, _ := find.Compile("ababaca", 7)
	results = append(results, m7.SelfCheck("abababaca") == nil)
	_, e1 := find.Compile("", 8)
	bad, e2 := find.Compile("too-long", 3)
	_, e3 := m.FindAll("a")
	results = append(results, errors.Is(e1, find.ErrEmptyPattern) && errors.Is(e2, find.ErrPatternTooLong) && errors.Is(e3, find.ErrTextShorterThanText) && bad == nil)
	after, _ := m.FindAll("aaaa")
	results = append(results, reflect.DeepEqual(after, overlap))

	linear := true
	for _, size := range [][2]int{{1000, 10}, {100000, 100}} {
		bigText := strings.Repeat("a", size[0])
		bigPattern := strings.Repeat("a", size[1]-1) + "b"
		bigTable, _ := table.New(bigPattern)
		bigScanner := scan.New(bigTable)
		bigScanner.Scan(bigText)
		linear = linear && counter(&bigScanner, "textAdvances") == size[0]
		linear = linear && counter(&bigScanner, "comparisons") <= 2*size[0]
	}
	results = append(results, linear)

	const workers = 16
	var wg sync.WaitGroup
	concurrent := make([][]int, workers)
	for i := range concurrent {
		wg.Add(1)
		go func(i int) { defer wg.Done(); concurrent[i], _ = m.FindAll("aaaaaaaaaa") }(i)
	}
	wg.Wait()
	same := true
	for _, got := range concurrent {
		same = same && reflect.DeepEqual(got, concurrent[0])
	}
	results = append(results, same)

	labels := []string{"derived table", "derived match", "overlap", "naive agreement", "self-check", "three errors", "rejection no trace", "linear counters", "concurrent"}
	for i, label := range labels {
		fmt.Println(status(results[i], label))
	}
}

func counter(s *scan.Scanner, name string) int {
	field := reflect.ValueOf(s).Elem().FieldByName(name)
	return int(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Int())
}

func status(ok bool, name string) string {
	if ok {
		return "OK " + name
	}
	return "FAIL " + name
}
