package main

import (
	"context"
	"fmt"

	"ontology/parse"
	"ontology/source"
)

func main() {
	failures := 0
	check("source resume position", checkSource(), &failures)
	check("bad records counted", checkParse(), &failures)
	fmt.Printf("TOTAL failures=%d\n", failures)
	if failures != 0 {
		panic("demo failed")
	}
}

func checkParse() bool {
	parser := &parse.Parser{}
	inputs := [][]byte{[]byte("=3"), []byte("missing"), []byte("x=not-a-number"), []byte("x=4")}
	good := 0
	for i, data := range inputs {
		if record, ok := parser.Parse(source.Raw{Pos: int64(i), Data: data}); ok {
			good++
			if i == 3 && (record.Key != "x" || record.Value != 4) {
				return false
			}
		}
	}
	return good == 2 && parser.Bad() == 2
}

func check(name string, ok bool, failures *int) {
	if ok {
		fmt.Printf("OK %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	*failures++
}

func checkSource() bool {
	records := [][]byte{[]byte("a"), []byte{1}, []byte("c")}
	src := source.New(source.Config{Records: records, EndAt: 2})
	if err := src.Open(1); err != nil {
		return false
	}
	raw, err := src.Read(context.Background())
	return err == nil && raw.Pos == 1 && len(raw.Data) == 1 && raw.Data[0] == 1
}
