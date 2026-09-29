// fulljoin-demo 演示全外连接增量维护器：打印每批输入、产出的变更日志、
// 当前视图与自检判定，便于人工核对穿越零切换规则。
package main

import (
	"fmt"

	"ontology/fulljoin"
)

func main() {
	m := fulljoin.New()

	batches := []struct {
		tag string
		cs  []fulljoin.Change
	}{
		{"左侧一行（右空位补位）", []fulljoin.Change{
			{Kind: '+', Side: fulljoin.SideLeft, Row: fulljoin.Row{Key: "k", ID: "l1", Val: "L1"}},
		}},
		{"右侧到达，计数 0->1 穿越", []fulljoin.Change{
			{Kind: '+', Side: fulljoin.SideRight, Row: fulljoin.Row{Key: "k", ID: "r1", Val: "R1"}},
		}},
		{"右侧再来一行（非穿越）", []fulljoin.Change{
			{Kind: '+', Side: fulljoin.SideRight, Row: fulljoin.Row{Key: "k", ID: "r2", Val: "R2"}},
		}},
		{"左侧离开，计数 1->0 穿越", []fulljoin.Change{
			{Kind: '-', Side: fulljoin.SideLeft, Row: fulljoin.Row{Key: "k", ID: "l1"}},
		}},
		{"整批非法：重复 ID，原子回滚", []fulljoin.Change{
			{Kind: '+', Side: fulljoin.SideRight, Row: fulljoin.Row{Key: "k", ID: "r3", Val: "R3"}},
			{Kind: '+', Side: fulljoin.SideRight, Row: fulljoin.Row{Key: "k", ID: "r3", Val: "dup"}},
		}},
	}

	for _, b := range batches {
		fmt.Printf("== %s ==\n输入: %v\n", b.tag, b.cs)
		es, err := m.Apply(b.cs)
		if err != nil {
			fmt.Printf("结果: 整批拒绝 -> %v\n判定: %s\n\n", err, classify(err))
			continue
		}
		for _, e := range es {
			fmt.Printf("日志: %s\n", e)
		}
		fmt.Println("视图:")
		for _, o := range m.View() {
			fmt.Printf("  %s\n", o)
		}
		if err := m.Check(); err != nil {
			fmt.Printf("自检: 失败 %v\n", err)
			return
		}
		fmt.Print("自检: 视图 == 批量重算 == 日志重放\n\n")
	}
}

func classify(err error) string {
	switch {
	case fulljoin.IsDuplicateID(err):
		return "重复插入行标识 ErrDuplicateID，状态与日志不变"
	case fulljoin.IsRowNotFound(err):
		return "删除不存在行标识 ErrRowNotFound，状态与日志不变"
	case fulljoin.IsEmptyKey(err):
		return "键为空 ErrEmptyKey，状态与日志不变"
	default:
		return err.Error()
	}
}
