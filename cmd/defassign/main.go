// Command defassign 从命令行对一个或多个 DSL 程序做确定赋值检查。
//
// 用法：
//
//	defassign [-c jobs] file1 [file2 ...]
//
// 每个文件是一个独立程序，多个程序并发检查、结果互不影响。
// 输出逐条记录「输入 / 实际输出 / 判定依据」，字节稳定，可用于审计。
package main

import (
	"flag"
	"fmt"
	"os"
	"sync"

	"ontology/defassign"
)

func main() {
	jobs := flag.Int("c", 0, "并发检查的程序数（0=每程序一个 goroutine）")
	flag.Parse()
	paths := flag.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "usage: defassign [-c jobs] file [file ...]")
		os.Exit(2)
	}

	sem := make(chan struct{}, len(paths))
	if *jobs > 0 && *jobs < len(paths) {
		sem = make(chan struct{}, *jobs)
	}
	var wg sync.WaitGroup
	results := make([]string, len(paths))
	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = checkOne(path)
		}(i, path)
	}
	wg.Wait()
	for _, r := range results {
		fmt.Print(r)
	}
}

func checkOne(path string) string {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("==== program %s\ninput: <unreadable: %v>\n", path, err)
	}
	rep, err := defassign.CheckText(path, string(src))
	var out string
	if err != nil {
		out = fmt.Sprintf("rejected: %v\n", err)
	} else {
		out = rep.Text()
		for _, d := range rep.Diags {
			out += fmt.Sprintf("  basis %s %s: %v\n", d.Kind, d.Var, d.Witness)
		}
	}
	return fmt.Sprintf("==== program %s\n---- input ----\n%s---- result ----\n%s",
		path, string(src), out)
}
