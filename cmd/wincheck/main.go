// wincheck 读取 JSON 事件文件，用引擎跑一遍并用独立模型批量重算，
// 核对每个窗口的最终计数是否一致。
//
// 用法：
//
//	go run ./cmd/wincheck events.json
//
// events.json 格式：
//
//	{
//	  "config": {"windowSize":10,"earlyEvery":2,"watermarkDelay":0,"allowedLateness":5,"maxOpenWindows":100},
//	  "batches": [[{"key":"a","timestamp":1}, ...], ...]
//	}
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"ontology/window"
)

type input struct {
	Config struct {
		WindowSize      int64 `json:"windowSize"`
		EarlyEvery      int64 `json:"earlyEvery"`
		WatermarkDelay  int64 `json:"watermarkDelay"`
		AllowedLateness int64 `json:"allowedLateness"`
		MaxOpenWindows  int   `json:"maxOpenWindows"`
	} `json:"config"`
	Batches [][]struct {
		Key       string `json:"key"`
		Timestamp int64  `json:"timestamp"`
	} `json:"batches"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: wincheck <events.json>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取文件失败:", err)
		os.Exit(1)
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "解析 JSON 失败:", err)
		os.Exit(1)
	}
	cfg := window.Config{
		WindowSize:      in.Config.WindowSize,
		EarlyEvery:      in.Config.EarlyEvery,
		WatermarkDelay:  in.Config.WatermarkDelay,
		AllowedLateness: in.Config.AllowedLateness,
		MaxOpenWindows:  in.Config.MaxOpenWindows,
	}
	eng, err := window.NewEngine(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置非法:", err)
		os.Exit(1)
	}

	var all []window.Event
	var changelog []window.Change
	for i, batch := range in.Batches {
		events := make([]window.Event, len(batch))
		for j, b := range batch {
			events[j] = window.Event{Key: b.Key, Timestamp: b.Timestamp}
		}
		changes, err := eng.Ingest(events)
		if err != nil {
			fmt.Fprintf(os.Stderr, "第 %d 批被拒: %v\n", i, err)
			os.Exit(1)
		}
		all = append(all, events...)
		changelog = append(changelog, changes...)
	}

	if err := eng.SelfCheck(); err != nil {
		fmt.Fprintln(os.Stderr, "自检失败:", err)
		os.Exit(1)
	}

	got := eng.FinalCounts()
	want := window.RecomputeFinalCounts(all, cfg)
	key := func(f window.FinalCount) string {
		return fmt.Sprintf("%s@[%d,%d)", f.Key, f.WindowStart, f.WindowEnd)
	}
	gotMap := make(map[string]int64, len(got))
	for _, f := range got {
		gotMap[key(f)] = f.Count
	}
	wantMap := make(map[string]int64, len(want))
	for _, f := range want {
		wantMap[key(f)] = f.Count
	}

	names := make([]string, 0, len(wantMap))
	for k := range wantMap {
		names = append(names, k)
	}
	for k := range gotMap {
		if _, ok := wantMap[k]; !ok {
			names = append(names, k)
		}
	}
	sort.Strings(names)

	mismatch := 0
	for _, k := range names {
		g, gok := gotMap[k]
		w, wok := wantMap[k]
		status := "OK"
		if !gok || !wok || g != w {
			status = "MISMATCH"
			mismatch++
		}
		fmt.Printf("%-8s %-24s 引擎最终值=%d 重算值=%d\n", status, k, g, w)
	}
	fmt.Printf("变更日志 %d 条，丢弃 %d 条，窗口 %d 个，不一致 %d 个\n",
		len(changelog), eng.Dropped(), len(names), mismatch)
	if mismatch > 0 {
		os.Exit(1)
	}
}
