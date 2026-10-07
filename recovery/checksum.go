package recovery

import (
	"hash/fnv"
	"sort"
	"strconv"
)

// SnapshotChecksum 计算单条快照记录的校验信息。
// 校验对象为 (对象ID, 状态) 的完整内容：任何字段被篡改都会失配。
func SnapshotChecksum(obj ObjectID, state State) Checksum {
	h := fnv.New64a()
	_, _ = h.Write([]byte("snap\x00"))
	_, _ = h.Write([]byte(obj))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(state))
	return strconv.FormatUint(h.Sum64(), 16)
}

// ActionChecksum 计算一条动作记录完整负载的校验信息。
// 涉及的对象集合按 ID 排序后参与哈希，保证与 map 遍历顺序无关，
// 同时使“只损坏其中一个对象的效果字段”也必然导致整条失配。
func ActionChecksum(id ActionID, base Version, effects map[ObjectID]Effect) Checksum {
	ids := make([]ObjectID, 0, len(effects))
	for obj := range effects {
		ids = append(ids, obj)
	}
	sort.Strings(ids)
	h := fnv.New64a()
	_, _ = h.Write([]byte("act\x00"))
	_, _ = h.Write([]byte(id))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(base))
	for _, obj := range ids {
		ef := effects[obj]
		_, _ = h.Write([]byte("\x00o\x00"))
		_, _ = h.Write([]byte(obj))
		_, _ = h.Write([]byte("\x00h\x00"))
		if ef.HasStart {
			_, _ = h.Write([]byte("1"))
		} else {
			_, _ = h.Write([]byte("0"))
		}
		_, _ = h.Write([]byte("\x00s\x00"))
		_, _ = h.Write([]byte(ef.Start))
		_, _ = h.Write([]byte("\x00c\x00"))
		_, _ = h.Write([]byte(ef.Change))
	}
	return strconv.FormatUint(h.Sum64(), 16)
}
