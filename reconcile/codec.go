package reconcile

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"sort"
	"strconv"
	"strings"
)

// 快照的信封线格式：头部携带副本标识、优先标识与逻辑位点，
// 头部与每个条目各自带 CRC32 校验和，使结构性损坏可以被
// 定位到“整体不可读”或“局部不可读”两种粒度。

type envelopeJSON struct {
	Header  headerJSON  `json:"header"`
	Entries []entryJSON `json:"entries"`
}

type headerJSON struct {
	ReplicaID string `json:"replica_id"`
	Priority  uint64 `json:"priority"`
	Position  uint64 `json:"position"`
	Checksum  uint32 `json:"checksum"`
}

type entryJSON struct {
	ObjectID   string                   `json:"object_id"`
	Attributes map[string]attributeJSON `json:"attributes"`
	Checksum   uint32                   `json:"checksum"`
}

type attributeJSON struct {
	Value     string `json:"value"`
	WrittenAt uint64 `json:"written_at"`
}

func headerChecksum(h headerJSON) uint32 {
	s := strings.Join([]string{
		h.ReplicaID,
		strconv.FormatUint(h.Priority, 10),
		strconv.FormatUint(h.Position, 10),
	}, "\x00")
	return crc32.ChecksumIEEE([]byte(s))
}

func entryChecksum(e entryJSON) uint32 {
	names := make([]string, 0, len(e.Attributes))
	for name := range e.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString(e.ObjectID)
	for _, name := range names {
		a := e.Attributes[name]
		sb.WriteString("\x00")
		sb.WriteString(name)
		sb.WriteString("\x00")
		sb.WriteString(a.Value)
		sb.WriteString("\x00")
		sb.WriteString(strconv.FormatUint(a.WrittenAt, 10))
	}
	return crc32.ChecksumIEEE([]byte(sb.String()))
}

// EncodeSnapshot 将快照序列化为信封格式，供副本侧产出与测试构造输入。
func EncodeSnapshot(s Snapshot) []byte {
	env := envelopeJSON{
		Header: headerJSON{
			ReplicaID: s.ReplicaID,
			Priority:  s.Priority,
			Position:  s.Position,
		},
	}
	env.Header.Checksum = headerChecksum(env.Header)

	for _, obj := range s.Objects {
		e := entryJSON{
			ObjectID:   obj.ObjectID,
			Attributes: make(map[string]attributeJSON, len(obj.Attributes)),
		}
		for name, a := range obj.Attributes {
			e.Attributes[name] = attributeJSON{Value: a.Value, WrittenAt: a.WrittenAt}
		}
		e.Checksum = entryChecksum(e)
		env.Entries = append(env.Entries, e)
	}

	data, err := json.Marshal(env)
	if err != nil {
		panic(fmt.Sprintf("reconcile: 快照编码失败: %v", err))
	}
	return data
}

// decodedSnapshot 是单个信封的解析结果。
type decodedSnapshot struct {
	// headerOK 为 false 表示整体不可读，其余字段均不可信。
	headerOK bool
	snap     Snapshot
	// badEntries 是校验失败的条目（局部不可读）的对象 ID。
	badEntries []string
}

// decodeEnvelope 解析单个信封，不返回错误：任何结构性问题都
// 体现在 decodedSnapshot 中，交由隔离层统一裁决。
func decodeEnvelope(blob []byte) decodedSnapshot {
	var env envelopeJSON
	if err := json.Unmarshal(blob, &env); err != nil {
		return decodedSnapshot{}
	}
	if env.Header.Checksum != headerChecksum(env.Header) {
		return decodedSnapshot{}
	}

	d := decodedSnapshot{
		headerOK: true,
		snap: Snapshot{
			ReplicaID: env.Header.ReplicaID,
			Priority:  env.Header.Priority,
			Position:  env.Header.Position,
		},
	}
	for _, e := range env.Entries {
		if e.Checksum != entryChecksum(e) {
			d.badEntries = append(d.badEntries, e.ObjectID)
			continue
		}
		obj := ObjectEntry{
			ObjectID:   e.ObjectID,
			Attributes: make(map[string]AttributeValue, len(e.Attributes)),
		}
		for name, a := range e.Attributes {
			obj.Attributes[name] = AttributeValue{Value: a.Value, WrittenAt: a.WrittenAt}
		}
		d.snap.Objects = append(d.snap.Objects, obj)
	}
	sort.Strings(d.badEntries)
	return d
}
