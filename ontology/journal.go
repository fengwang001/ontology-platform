package ontology

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
)

// WAL 按批次分段：每个批次一个独立段文件 journal/<id>.jlog。
//
// 这一布局是“恢复扫描量只与本批次规模相关”的关键：
// 恢复时只需读取 CONTROL 中登记的那一个活跃段，无需触碰任何历史段。
// 批次干净结束后段文件被删除，磁盘上不会累积历史日志。

// journalRecordType 枚举段内记录类型。
type journalRecordType string

const (
	recBegin  journalRecordType = "BEGIN"  // 批次出生证明：写集与全部变更
	recMut    journalRecordType = "MUT"    // 单个变更的前像/后像
	recCommit journalRecordType = "COMMIT" // 唯一的“已被外部认定为已生效”判定记录
	recDone   journalRecordType = "DONE"   // 批次收尾完成
)

// image 是某实例在变更前/后的完整状态（版本号 + 属性 + 批次戳）。
type image struct {
	Version   uint64            `json:"version"`
	LastBatch BatchID           `json:"batch"`
	Props     map[string]string `json:"props"`
	Exists    bool              `json:"exists"`
}

// journalRecord 是段内的一行（JSON + CRC）。
type journalRecord struct {
	Type     journalRecordType `json:"type"`
	Batch    BatchID           `json:"batch"`
	Seq      int               `json:"seq"`            // MUT 记录内变更下标
	Instance InstanceID        `json:"instance"`       // MUT 记录的目标实例
	Before   image             `json:"before"`         // 前像（用于回滚）
	After    image             `json:"after"`          // 后像（用于重做）
	Muts     []Mutation        `json:"muts,omitempty"` // BEGIN 记录携带的完整变更集
	CRC      uint32            `json:"crc"`
}

func (r *journalRecord) seal() {
	r.CRC = 0
	payload, _ := json.Marshal(r)
	r.CRC = crc32.ChecksumIEEE(payload)
}

func (r *journalRecord) valid() bool {
	want := r.CRC
	r.seal()
	ok := r.CRC == want
	r.CRC = want
	return ok
}

func segmentFile(id BatchID) string {
	return fmt.Sprintf("journal/%08d.jlog", uint64(id))
}

// journalWriter 追加写入一个批次段。
type journalWriter struct {
	disk Disk
	file string
}

func newJournalWriter(disk Disk, id BatchID) *journalWriter {
	return &journalWriter{disk: disk, file: segmentFile(id)}
}

func (w *journalWriter) append(rec *journalRecord) {
	rec.seal()
	line, _ := json.Marshal(rec)
	w.disk.AppendFile(w.file, append(line, '\n'))
}

func (w *journalWriter) sync() {
	w.disk.Sync(w.file)
}

// journalData 是扫描一个批次段得到的全部有效记录。
type journalData struct {
	Records    []journalRecord // 按写入顺序的有效记录（坏尾被截断）
	Begin      *journalRecord
	Muts       []journalRecord
	HasCommit  bool
	HasDone    bool
	Truncated  bool // 是否截断了损坏尾部
	BytesRead  int  // 实际读取的字节数（扫描规模的证据）
	SegmentErr error
}

// scanJournal 读取并校验一个批次段；段不存在时返回空数据。
func scanJournal(disk Disk, id BatchID) *journalData {
	out := &journalData{}
	data, err := disk.ReadFile(segmentFile(id))
	if err == ErrNotFound {
		return out
	}
	if err != nil {
		out.SegmentErr = err
		return out
	}
	out.BytesRead = len(data)
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec journalRecord
		if err := json.Unmarshal(line, &rec); err != nil || !rec.valid() {
			out.Truncated = true
			break // 崩溃造成的撕裂尾部：之后的记录一律视为不存在
		}
		out.Records = append(out.Records, rec)
		switch rec.Type {
		case recBegin:
			b := rec
			out.Begin = &b
		case recMut:
			out.Muts = append(out.Muts, rec)
		case recCommit:
			out.HasCommit = true
		case recDone:
			out.HasDone = true
		}
	}
	return out
}
