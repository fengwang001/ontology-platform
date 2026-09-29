// 多视图全局一致读的可运行演示：逐步打印输入、一致时间点与判定依据。
//
// 运行：go run ./cmd/demo
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"ontology/consistency"
)

func main() {
	ctx := context.Background()
	logger := log.New(log.Writer(), "", log.Ltime|log.Lmicroseconds)

	store, err := consistency.NewStore(2, "orders", "inventory")
	if err != nil {
		log.Fatal(err)
	}
	store.SetLogger(logger.Printf)

	try := func(desc string, err error) {
		if err != nil {
			fmt.Printf("  -> %-28s 被拒绝：%v\n", desc, classify(err))
		} else {
			fmt.Printf("  -> %-28s 已接受\n", desc)
		}
	}

	show := func(desc string, snap *consistency.Snapshot, err error) {
		if err != nil {
			fmt.Printf("  -> %-28s 被拒绝：%v\n", desc, classify(err))
			return
		}
		fmt.Printf("  -> %-28s 一致时间点 t=%d：", desc, snap.At)
		for i, v := range snap.Views {
			if i > 0 {
				fmt.Print("；")
			}
			fmt.Printf("%s=%v(版本 t=%d)", v.View, v.Value, v.VersionTs)
		}
		fmt.Println()
	}
	readAt := func(desc string, at consistency.Timestamp) {
		snap, err := store.ReadAt(ctx, at)
		show(desc, snap, err)
	}
	read := func(desc string) {
		snap, err := store.Read(ctx)
		show(desc, snap, err)
	}

	fmt.Println("== 1. 两个视图各自应用，进度不同 ==")
	try("orders@t=1", store.Apply(ctx, "orders", 1, "O1"))
	try("inventory@t=2", store.Apply(ctx, "inventory", 2, "I2"))
	readAt("读取 t=2（超过进度最小值）", 2)
	readAt("读取 t=1（inventory 首版本更晚）", 1)
	try("orders@t=2 补齐同点版本", store.Apply(ctx, "orders", 2, "O2"))
	readAt("读取 t=2（此时的最早一致点）", 2)

	fmt.Println("== 2. 心跳只推进进度，不产生版本 ==")
	try("orders 心跳 t=3", store.Heartbeat(ctx, "orders", 3))
	try("orders 重复心跳 t=3", store.Heartbeat(ctx, "orders", 3))
	read("自动读取（最新一致点 t=2）")

	fmt.Println("== 3. 淘汰导致太旧 ==")
	try("orders@t=4", store.Apply(ctx, "orders", 4, "O4"))
	try("inventory@t=4", store.Apply(ctx, "inventory", 4, "I4"))
	try("orders@t=5", store.Apply(ctx, "orders", 5, "O5"))
	try("inventory@t=5", store.Apply(ctx, "inventory", 5, "I5"))
	readAt("读取 t=1（版本已淘汰）", 1)
	readAt("读取 t=4（仍在保留窗口）", 4)

	fmt.Println("== 4. 非法与不前进输入 ==")
	try("未知视图", store.Apply(ctx, "billing", 6, "X"))
	try("nil 值", store.Apply(ctx, "orders", 6, nil))
	try("时间戳倒退", store.Apply(ctx, "orders", 2, "OLD"))

	fmt.Println("== 5. 连续自动读取单调不减 ==")
	read("自动读取 A")
	try("inventory 心跳 t=8", store.Heartbeat(ctx, "inventory", 8))
	read("自动读取 B（不回退）")
}

func classify(err error) string {
	switch {
	case errors.Is(err, consistency.ErrInvalidArgument):
		return "非法参数"
	case errors.Is(err, consistency.ErrTimestampNotAdvancing):
		return "时间戳未前进"
	case errors.Is(err, consistency.ErrNotReady):
		return "尚未准备好（超过各视图进度最小值）"
	case errors.Is(err, consistency.ErrTooOld):
		return "太旧（已超出保留历史）"
	default:
		return err.Error()
	}
}
