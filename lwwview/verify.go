package lwwview

import (
	"fmt"
	"sort"
)

// ExpectedWinner 是独立重算得到的某个键的最终仲裁结果。
type ExpectedWinner struct {
	// EventIndex 是获胜事件在输入批次中的下标（同事件时间先到者胜）。
	EventIndex int
	Entry      Entry
}

// ExpectedByEventTime 完全按照事件时间仲裁规则独立重算一批事件的最终结果，
// 不依赖 View 的内部实现，可作为本地核对的基准（oracle）。
//
// 规则：按批次顺序扫描；事件时间严格更大者替换获胜者；事件时间相等时
// 保留先到者（下标更小者）；事件包含删除（Value == nil），因此结果键可能
// 是“不存在”墓碑；空值写入则是 Exists=true、Value="" 的存在记录。
func ExpectedByEventTime(events []Event) (map[string]ExpectedWinner, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("%w: empty batch", ErrInvalidArgument)
	}

	type winner struct {
		index int
		entry Entry
	}
	winners := make(map[string]winner)

	for i, event := range events {
		if event.Key == "" {
			return nil, &EventError{Index: i, Err: ErrEmptyKey}
		}

		entry := Entry{EventTime: event.EventTime}
		if event.Value != nil {
			entry.Exists = true
			entry.Value = *event.Value
		}

		current, ok := winners[event.Key]
		// 严格大于才替换：相等时保留先到者。
		if !ok || event.EventTime > current.entry.EventTime {
			winners[event.Key] = winner{index: i, entry: entry}
		}
	}

	out := make(map[string]ExpectedWinner, len(winners))
	for key, winner := range winners {
		out[key] = ExpectedWinner{EventIndex: winner.index, Entry: winner.entry}
	}
	return out, nil
}

// VerifyFreshBatch 将事件应用到一个全新视图，并按事件时间逐项核对：
// 每个键的最终 Entry 必须与独立重算的获胜者一致，同时通过 SelfCheck。
// 这是文档中“批量按事件时间核对结果”的本地验证方法的代码入口。
func VerifyFreshBatch(maxKeys int, events []Event) (*View, map[string]ExpectedWinner, error) {
	expected, err := ExpectedByEventTime(events)
	if err != nil {
		return nil, nil, err
	}

	tracked := len(expected)
	if maxKeys > 0 && tracked > maxKeys {
		return nil, nil, &BatchError{Reason: "too many keys", Inner: ErrTooManyKeys}
	}

	view := New(maxKeys)
	if _, _, applyErr := view.Apply(events); applyErr != nil {
		return nil, nil, applyErr
	}

	if checkErr := view.SelfCheck(); checkErr != nil {
		return nil, nil, checkErr
	}

	snapshot := view.Snapshot()
	expectedVisible := make(map[string]Entry, len(expected))
	for key, winner := range expected {
		if winner.Entry.Exists {
			expectedVisible[key] = winner.Entry
		}
	}
	if len(snapshot) != len(expectedVisible) {
		return nil, nil, fmt.Errorf("lwwview: verify key count mismatch: view %d, expected %d",
			len(snapshot), len(expectedVisible))
	}

	keys := make([]string, 0, len(expectedVisible))
	for key := range expectedVisible {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		got, ok := snapshot[key]
		if !ok {
			return nil, nil, fmt.Errorf("lwwview: verify missing key %q", key)
		}
		want := expectedVisible[key]
		if got != want {
			return nil, nil, fmt.Errorf("lwwview: verify mismatch for key %q: view=%+v expected=%+v",
				key, got, want)
		}
	}

	return view, expected, nil
}
