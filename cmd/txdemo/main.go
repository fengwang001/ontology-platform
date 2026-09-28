// Command txdemo 演示事务重组：从标准输入逐行读取事件，
// 按到达顺序送入 transaction.Reassembler，并把输入、输出与判定依据打印到日志。
//
// 事件行格式（空白分隔，# 开头为注释）：
//
//	BEGIN    <txID>
//	WRITE    <txID> <key> <value>
//	COMMIT   <txID>
//	ROLLBACK <txID>
//
// 不带任何参数且标准输入为空时，运行内置的交错事务示例。
// 可用 -limit 覆盖缓冲行上限（默认 16）。
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"ontology/transaction"
)

func main() {
	limit := flag.Int("limit", 16, "max buffered rows across all in-flight transactions")
	flag.Parse()

	var in io.Reader = os.Stdin
	// 终端且无重定向输入时，运行内置示例，方便直接 go run。
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		in = demo()
	}
	if err := run(in, os.Stdout, *limit); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// parseLine 把一行文本解析为事务事件。空行或注释行返回 ok=false。
func parseLine(line string) (ev transaction.Event, ok bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return transaction.Event{}, false, nil
	}
	fields := strings.Fields(line)
	op := strings.ToUpper(fields[0])
	switch op {
	case "BEGIN", "COMMIT", "ROLLBACK":
		if len(fields) != 2 {
			return transaction.Event{}, false, fmt.Errorf("%s needs exactly 1 field <txID>, got %d", op, len(fields)-1)
		}
		var typ transaction.EventType
		switch op {
		case "BEGIN":
			typ = transaction.EventBegin
		case "COMMIT":
			typ = transaction.EventCommit
		case "ROLLBACK":
			typ = transaction.EventRollback
		}
		return transaction.Event{Type: typ, TxID: fields[1]}, true, nil
	case "WRITE":
		if len(fields) != 4 {
			return transaction.Event{}, false, fmt.Errorf("WRITE needs exactly 3 fields <txID> <key> <value>, got %d", len(fields)-1)
		}
		return transaction.Event{
			Type: transaction.EventWrite,
			TxID: fields[1],
			Row:  transaction.Row{Key: fields[2], Value: fields[3]},
		}, true, nil
	default:
		return transaction.Event{}, false, fmt.Errorf("unknown op %q (want BEGIN/WRITE/COMMIT/ROLLBACK)", op)
	}
}

// run 从 in 逐行读取事件并送入重组器，把判定日志写到 out。
func run(in io.Reader, out io.Writer, limit int) error {
	r := transaction.NewReassembler(limit, transaction.WithLogger(out))

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		ev, ok, perr := parseLine(scanner.Text())
		if perr != nil {
			return fmt.Errorf("line %d: %w", lineNo, perr)
		}
		if !ok {
			continue
		}
		committed, aerr := r.Apply(ev)
		if aerr != nil {
			// 非法事件在重组器内已记录 rej 日志；这里补充面向用户的原因归类后继续，
			// 演示“拒绝不改变状态、序列继续处理”的语义。
			var rej *transaction.RejectError
			if errors.As(aerr, &rej) {
				fmt.Fprintf(out, "note line %d rejected: %s (%s)\n", lineNo, rej.Reason, rej.TxID)
				continue
			}
			return fmt.Errorf("line %d: %w", lineNo, aerr)
		}
		if committed != nil {
			fmt.Fprintf(out, ">>> delivered committed tx=%s seq=%d rows=%d\n",
				committed.TxID, committed.Seq, len(committed.Rows))
		}
	}
	if serr := scanner.Err(); serr != nil {
		return serr
	}
	fmt.Fprintf(out, "summary: inflight=%d buffered=%d\n", r.InFlight(), r.BufferedRows())
	return nil
}

// demo 内置的交错事务示例：t1/t2 交错写入，t3 回滚，含一个空事务与一个非法重复开始。
func demo() io.Reader {
	return strings.NewReader(`# 交错事务重组示例
BEGIN t1
BEGIN t2
WRITE t1 a 1
WRITE t2 x 9
WRITE t1 b 2
BEGIN t3
WRITE t3 junk z
BEGIN t1
WRITE t2 y 8
ROLLBACK t3
COMMIT t2
BEGIN t4
COMMIT t4
COMMIT t1
WRITE ghost nope 0
`)
}
