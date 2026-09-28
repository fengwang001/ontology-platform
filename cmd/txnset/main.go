// Command txnset 解析、规范化或求差事务标识集合文本。
//
// 用法：
//
//	txnset canonical "src-a:1,src-a:2-4"
//	txnset diff "db:1-100" "db:1-60,db:62-80"
//
// 加 --log 可在标准错误打印输入、规范文本与判定依据的结构化日志。
// 成功时规范文本输出到标准输出（空集合输出为空行）；
// 失败时错误信息输出到标准错误并以非零状态退出。
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"ontology/txnset"
)

func main() {
	withLog := flag.Bool("log", false, "print structured decision logs to stderr")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage:\n  %s canonical <text>\n  %s diff <source-text> <local-text>\n",
			os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *withLog {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		txnset.SetLogger(logger)
	}

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	switch args[0] {
	case "canonical":
		if len(args) != 2 {
			flag.Usage()
			os.Exit(2)
		}
		s, err := txnset.Parse(args[1])
		if err != nil {
			fail(err)
		}
		fmt.Println(s.Canonical())
	case "diff":
		if len(args) != 3 {
			flag.Usage()
			os.Exit(2)
		}
		source, err := txnset.Parse(args[1])
		if err != nil {
			fail(err)
		}
		local, err := txnset.Parse(args[2])
		if err != nil {
			fail(err)
		}
		fmt.Println(source.Diff(local).Canonical())
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func fail(err error) {
	var pe *txnset.ParseError
	if errors.As(err, &pe) {
		fmt.Fprintf(os.Stderr, "%s (offset=%d)\n", pe.Message, pe.Offset)
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(1)
}
