package ontology

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"sort"
)

// 实例存储采用“快照 + 增量日志”结构：
//
//   - store/snapshot.json：某一时刻的全量实例状态（检查点）；
//   - store/delta.log：追加式实例记录，每行一条带 CRC 的 JSON，
//     末尾的撕裂/损坏行在加载时被截断忽略。
//
// 批次干净结束或恢复收尾时执行检查点：重写快照并清空增量日志，
// 因此增量日志的规模只与“最近一个未决批次”相关，而不随历史批次总数增长。
const (
	snapshotFile = "store/snapshot.json"
	deltaFile    = "store/delta.log"
)

// deltaRecord 是增量日志中的一行。
type deltaRecord struct {
	ID        InstanceID        `json:"id"`
	Version   uint64            `json:"version"`
	LastBatch BatchID           `json:"batch"`
	Props     map[string]string `json:"props"`
	CRC       uint32            `json:"crc"`
}

func (r *deltaRecord) seal() {
	r.CRC = 0
	payload, _ := json.Marshal(r)
	r.CRC = crc32.ChecksumIEEE(payload)
}

func (r *deltaRecord) valid() bool {
	want := r.CRC
	r.seal()
	ok := r.CRC == want
	r.CRC = want
	return ok
}

// store 是实例存储：内存索引 + 磁盘持久化。
type store struct {
	disk Disk
	mem  map[InstanceID]Instance
}

// loadStore 从磁盘恢复实例索引：先读快照，再重放增量日志（截断坏尾）。
func loadStore(disk Disk) (*store, error) {
	s := &store{disk: disk, mem: make(map[InstanceID]Instance)}
	if data, err := disk.ReadFile(snapshotFile); err == nil {
		var snap struct {
			Instances []Instance `json:"instances"`
		}
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("ontology: corrupt snapshot: %w", err)
		}
		for _, in := range snap.Instances {
			s.mem[in.ID] = in
		}
	} else if err != ErrNotFound {
		return nil, err
	}
	if data, err := disk.ReadFile(deltaFile); err == nil {
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var rec deltaRecord
			if err := json.Unmarshal(line, &rec); err != nil || !rec.valid() {
				break // 撕裂或损坏的尾部：截断
			}
			s.mem[rec.ID] = Instance{
				ID:        rec.ID,
				Version:   rec.Version,
				LastBatch: rec.LastBatch,
				Props:     rec.Props,
			}
		}
	} else if err != ErrNotFound {
		return nil, err
	}
	return s, nil
}

// get 返回实例（拷贝），不存在时 ok=false。
func (s *store) get(id InstanceID) (Instance, bool) {
	in, ok := s.mem[id]
	return in.Clone(), ok
}

// apply 把实例的新状态写入内存索引并追加到增量日志（不 Sync）。
func (s *store) apply(in Instance) {
	s.mem[in.ID] = in.Clone()
	rec := deltaRecord{
		ID:        in.ID,
		Version:   in.Version,
		LastBatch: in.LastBatch,
		Props:     in.Props,
	}
	rec.seal()
	line, _ := json.Marshal(rec)
	s.disk.AppendFile(deltaFile, append(line, '\n'))
}

// syncDelta 把增量日志落盘。
func (s *store) syncDelta() {
	s.disk.Sync(deltaFile)
}

// checkpoint 重写全量快照并清空增量日志（原子替换 + 落盘）。
func (s *store) checkpoint() {
	ids := make([]string, 0, len(s.mem))
	for id := range s.mem {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	snap := struct {
		Instances []Instance `json:"instances"`
	}{Instances: make([]Instance, 0, len(ids))}
	for _, id := range ids {
		snap.Instances = append(snap.Instances, s.mem[InstanceID(id)])
	}
	data, _ := json.Marshal(snap)
	tmp := snapshotFile + ".tmp"
	s.disk.WriteFile(tmp, data)
	s.disk.Sync(tmp)
	s.disk.Rename(tmp, snapshotFile)
	s.disk.Delete(deltaFile)
}

// snapshot 返回当前全部实例的确定性视图（按 ID 排序）。
func (s *store) snapshot() []Instance {
	ids := make([]string, 0, len(s.mem))
	for id := range s.mem {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]Instance, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.mem[InstanceID(id)].Clone())
	}
	return out
}
