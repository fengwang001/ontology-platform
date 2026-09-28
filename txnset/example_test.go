package txnset_test

import (
	"errors"
	"fmt"
	"log"

	"ontology/txnset"
)

func ExampleParse() {
	// 任意顺序、重叠、相邻的输入都得到唯一规范文本。
	s, err := txnset.Parse("replica:5,replica:1-3,primary:9,replica:4-6")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.Canonical())
	// Output: primary:9,replica:1-6
}

func ExampleTxnSet_Diff() {
	// 复制断点续传：源端已执行集合减去本地已执行集合 = 尚待回放集合。
	source, err := txnset.Parse("db:1-100")
	if err != nil {
		log.Fatal(err)
	}
	local, err := txnset.Parse("db:1-60,db:62-80")
	if err != nil {
		log.Fatal(err)
	}
	pending := source.Diff(local)
	fmt.Println(pending.Canonical())
	// Output: db:61,db:81-100
}

func ExampleParse_errorClassification() {
	for _, text := range []string{"src:1 ", "9src:1", "src:01", "src:5-1"} {
		_, err := txnset.Parse(text)
		switch {
		case err == nil:
			fmt.Println("ok")
		case errors.Is(err, txnset.ErrSyntax):
			fmt.Println("syntax")
		case errors.Is(err, txnset.ErrIdentifier):
			fmt.Println("identifier")
		case errors.Is(err, txnset.ErrNumber):
			fmt.Println("number")
		}
	}
	// Output:
	// syntax
	// identifier
	// number
	// number
}
