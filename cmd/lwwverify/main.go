// lwwverify 是“批量按事件时间核对结果”的本地验证工具。
//
// 它读取一批事件（JSON Lines 或内置样例），分别用：
//  1. lwwview.View 实际物化；
//  2. lwwview.ExpectedByEventTime 独立重算仲裁结果（oracle）；
//
// 然后逐键核对值/事件时间/存在性，并重放变更日志做自检。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"ontology/lwwview"
)

type jsonEvent struct {
	Key       string  `json:"key"`
	EventTime int64   `json:"event_time"`
	Value     *string `json:"value"`
}

func sampleEvents() []lwwview.Event {
	empty := ""
	v1, v2, v3 := "alpha", "beta", "alpha-late"
	return []lwwview.Event{
		{Key: "user:1", EventTime: 10, Value: &v1},
		{Key: "user:2", EventTime: 5, Value: &v2},
		{Key: "user:1", EventTime: 10, Value: &v3}, // 同事件时间，先到者胜 -> alpha
		{Key: "user:2", EventTime: 9, Value: nil},  // 删除生效（9 > 5）
		{Key: "user:3", EventTime: 7, Value: &empty},
		{Key: "user:1", EventTime: 8, Value: &v3}, // 迟到（8 < 10），忽略
	}
}

func loadEvents(path string) ([]lwwview.Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	events := make([]lwwview.Event, 0)
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var je jsonEvent
		if err := json.Unmarshal(raw, &je); err != nil {
			return nil, fmt.Errorf("第 %d 行 JSON 解析失败: %w", line, err)
		}
		events = append(events, lwwview.Event{Key: je.Key, EventTime: je.EventTime, Value: je.Value})
	}
	return events, scanner.Err()
}

func main() {
	filePath := flag.String("file", "", "JSON Lines 事件文件路径（每行 {\"key\",\"event_time\",\"value\"}；value 省略或 null 表示删除）")
	maxKeys := flag.Int("max-keys", 0, "最大不同键数，0 表示使用事件去重后的数量")
	flag.Parse()

	events := sampleEvents()
	if *filePath != "" {
		loaded, err := loadEvents(*filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取事件失败: %v\n", err)
			os.Exit(2)
		}
		events = loaded
	}

	expected, err := lwwview.ExpectedByEventTime(events)
	if err != nil {
		fmt.Fprintf(os.Stderr, "批次非法，整批拒绝: %v\n", err)
		os.Exit(2)
	}

	capacity := *maxKeys
	if capacity == 0 {
		capacity = len(expected)
	}

	view, _, err := lwwview.VerifyFreshBatch(capacity, events)
	if err != nil {
		fmt.Fprintf(os.Stderr, "核对失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("输入事件（按到达顺序）:")
	for i, event := range events {
		op := "DELETE"
		if event.Value != nil {
			op = fmt.Sprintf("WRITE(%q)", *event.Value)
		}
		fmt.Printf("  [%d] eventTime=%-4d key=%-8s %s\n", i, event.EventTime, event.Key, op)
	}

	fmt.Println("\n按事件时间独立重算的仲裁结果（oracle）:")
	for key, winner := range expected {
		state := "不存在(墓碑)"
		if winner.Entry.Exists {
			state = fmt.Sprintf("存在 value=%q", winner.Entry.Value)
		}
		fmt.Printf("  key=%-8s eventTime=%-4d 获胜事件下标=%d %s\n",
			key, winner.Entry.EventTime, winner.EventIndex, state)
	}

	fmt.Println("\n视图最终键值（仅存在键，按 key 排序）:")
	for _, kv := range view.Entries() {
		fmt.Printf("  key=%-8s eventTime=%-4d value=%q\n", kv.Key, kv.EventTime, kv.Value)
	}

	fmt.Printf("\n迟到忽略 dropped=%d，变更日志条数=%d\n", view.Dropped(), len(view.Changelog()))
	if err := view.SelfCheck(); err != nil {
		fmt.Fprintf(os.Stderr, "自检失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("核对通过：视图结果与按事件时间独立重算逐项一致，变更日志重放自检通过。")
}
