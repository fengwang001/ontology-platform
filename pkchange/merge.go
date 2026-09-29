package pkchange

import (
	"hash/fnv"
)

// Merge 合并拆分序列：同一主键只保留它在拆分序列中的最后一条事件。
// 输出顺序按各键“最后一次出现”的先后排列，保证结果确定且与输入一一对应。
func Merge(events []Event) []Event {
	lastIndex := make(map[string]int, len(events))
	for i, ev := range events {
		lastIndex[ev.Key] = i
	}

	merged := make([]Event, 0, len(lastIndex))
	for i, ev := range events {
		if lastIndex[ev.Key] == i {
			merged = append(merged, cloneEvent(ev))
		}
	}
	return merged
}

// PartitionByKey 按主键哈希将事件分配到 partitionCount 个分区，
// 每个分区内部保持 merged 中的顺序（即拆分序列确定的顺序）。
// partitionCount 必须大于 0。
func PartitionByKey(events []Event, partitionCount int) []Partition {
	if partitionCount <= 0 {
		panic("pkchange: partitionCount must be positive")
	}
	partitions := make([]Partition, partitionCount)
	for i := range partitions {
		partitions[i] = Partition{Index: i}
	}
	for _, ev := range events {
		idx := KeyPartition(ev.Key, partitionCount)
		partitions[idx].Events = append(partitions[idx].Events, cloneEvent(ev))
	}
	return partitions
}

// KeyPartition 使用 FNV-1a 哈希返回主键所属分区下标，同一键永远落到同一分区。
func KeyPartition(key string, partitionCount int) int {
	if partitionCount <= 0 {
		panic("pkchange: partitionCount must be positive")
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(partitionCount))
}

func cloneEvent(ev Event) Event {
	var data Row
	if ev.Data != nil {
		data = cloneRow(ev.Data)
	}
	return Event{Kind: ev.Kind, Key: ev.Key, Data: data}
}
