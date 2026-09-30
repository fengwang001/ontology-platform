// 命令行入口：从 stdin 读取一份历史 JSON，打印判定结果与依据，
// 并演示多种异常类别。不带参数时运行内置演示；带 -in 时读取文件。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"ontology/anomaly"
)

func main() {
	in := flag.String("in", "", "历史 JSON 文件路径；缺省时运行内置演示")
	flag.Parse()

	logger := anomaly.NewLogger(os.Stdout)
	if *in != "" {
		data, err := os.ReadFile(*in)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var h anomaly.History
		if err := json.Unmarshal(data, &h); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		r := logger.Analyze(h)
		fmt.Printf("=> %s (%s)\n", r.Category, r.Level)
		return
	}

	for _, d := range demos() {
		fmt.Printf("==== %s ====\n", d.name)
		r := logger.Analyze(d.h)
		fmt.Printf("=> %s (%s)\n\n", r.Category, r.Level)
	}
}

type demo struct {
	name string
	h    anomaly.History
}

func demos() []demo {
	v := func(t, n int) [2]int { return [2]int{t, n} }
	rd := func(k string, t, n int) anomaly.Op {
		ver := v(t, n)
		return anomaly.Op{Type: "read", Key: k, ReadVersion: &ver}
	}
	wr := func(k string) anomaly.Op { return anomaly.Op{Type: "write", Key: k} }

	return []demo{
		{
			name: "丢失更新 (G-single)",
			h: anomaly.History{
				Txns: []anomaly.Txn{
					{ID: 1, Status: anomaly.Committed, Ops: []anomaly.Op{rd("x", 0, 0), wr("x")}},
					{ID: 2, Status: anomaly.Committed, Ops: []anomaly.Op{rd("x", 0, 0), wr("x")}},
				},
				Order: map[string][][2]int{"x": {v(0, 0), v(1, 1), v(2, 1)}},
			},
		},
		{
			name: "写偏斜 (G2)",
			h: anomaly.History{
				Txns: []anomaly.Txn{
					{ID: 1, Status: anomaly.Committed, Ops: []anomaly.Op{rd("x", 0, 0), rd("y", 0, 0), wr("x")}},
					{ID: 2, Status: anomaly.Committed, Ops: []anomaly.Op{rd("x", 0, 0), rd("y", 0, 0), wr("y")}},
				},
				Order: map[string][][2]int{
					"x": {v(0, 0), v(1, 1)},
					"y": {v(0, 0), v(2, 1)},
				},
			},
		},
		{
			name: "纯写写环 (G0)",
			h: anomaly.History{
				Txns: []anomaly.Txn{
					{ID: 1, Status: anomaly.Committed, Ops: []anomaly.Op{wr("x"), wr("y")}},
					{ID: 2, Status: anomaly.Committed, Ops: []anomaly.Op{wr("y"), wr("x")}},
				},
				Order: map[string][][2]int{
					"x": {v(0, 0), v(1, 1), v(2, 1)},
					"y": {v(0, 0), v(2, 1), v(1, 1)},
				},
			},
		},
	}
}
