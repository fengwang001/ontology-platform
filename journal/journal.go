// Package journal 负责执行轨迹的追加与重放。
// 记录带步骤 ID、阶段与单调序号；帧带长度与 CRC，半条记录在重放时被确定性丢弃。
// 存储只在进程内存中，但通过崩溃钩子模拟「写入前 / 写入后」崩溃。
package journal

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
)

// Phase 是一条记录所代表的步骤阶段转换。
type Phase string

const (
	Running          Phase = "running"
	Completed        Phase = "completed"
	Failed           Phase = "failed"
	Compensating     Phase = "compensating"
	Compensated      Phase = "compensated"
	CompensateFailed Phase = "compensate_failed"
)

// Record 是一条执行轨迹记录。
type Record struct {
	Seq    int64  `json:"seq"`
	StepID string `json:"step"`
	Phase  Phase  `json:"phase"`
	Err    string `json:"err,omitempty"`
}

// ErrJournalFull 在轨迹长度达到上限、拒绝追加时返回（已有字节不变）。
var ErrJournalFull = errors.New("journal: length limit reached")

// CrashHook 在每条记录写入前/后被调用；钩子返回错误则以此 panic 中止进程语义。
type CrashHook func(r Record, point Point)

// Point 标识崩溃钩子的位置。
type Point int

const (
	BeforeAppend Point = iota
	AfterAppend
)

// Store 是内存中的轨迹存储。
type Store struct {
	data     []byte
	maxBytes int
	seq      int64
	hook     CrashHook
}

// NewStore 创建存储；maxBytes<=0 表示不限长度。
func NewStore(maxBytes int) *Store {
	return &Store{maxBytes: maxBytes}
}

// SetCrashHook 安装崩溃钩子（编排器注入，用于故障注入测试）。
func (s *Store) SetCrashHook(h CrashHook) { s.hook = h }

// Len 返回当前已提交轨迹的字节长度。
func (s *Store) Len() int { return len(s.data) }

// LastSeq 返回最后一条记录的序号；无记录返回 0。
func (s *Store) LastSeq() int64 { return s.seq }

// Bytes 返回已提交字节的拷贝（用于半条记录的判定测试）。
func (s *Store) Bytes() []byte { return append([]byte(nil), s.data...) }

// AppendRaw 把任意字节当作一帧的负载追加，用于构造「半条记录」。
func (s *Store) AppendRaw(p []byte) error {
	return s.commit(p)
}

// Append 追加一条记录并返回最终序号。拒绝超限不改变任何字节。
func (s *Store) Append(stepID string, phase Phase, errMsg string) (Record, error) {
	r := Record{Seq: s.seq + 1, StepID: stepID, Phase: phase, Err: errMsg}
	payload, err := json.Marshal(r)
	if err != nil {
		return Record{}, err
	}
	frame := encodeFrame(payload)
	if s.maxBytes > 0 && len(s.data)+len(frame) > s.maxBytes {
		return Record{}, ErrJournalFull
	}
	if s.hook != nil {
		s.hook(r, BeforeAppend)
	}
	if err := s.commit(frame); err != nil {
		return Record{}, err
	}
	s.seq = r.Seq
	if s.hook != nil {
		s.hook(r, AfterAppend)
	}
	return r, nil
}

func (s *Store) commit(frame []byte) error {
	if s.maxBytes > 0 && len(s.data)+len(frame) > s.maxBytes {
		return ErrJournalFull
	}
	s.data = append(s.data, frame...)
	return nil
}

// Replay 重放全部有效记录；遇到半帧/损坏帧立即停止并丢弃其后一切字节。
// 同一字节序列重放任意次结果相同（纯解析，无副作用）。
func (s *Store) Replay() ([]Record, error) {
	return Replay(s.data)
}

func encodeFrame(payload []byte) []byte {
	n := uint32(len(payload))
	sum := crc32.ChecksumIEEE(payload)
	out := make([]byte, 4+4+len(payload)+4)
	binary.BigEndian.PutUint32(out[0:4], n)
	binary.BigEndian.PutUint32(out[4:8], sum)
	copy(out[8:], payload)
	binary.BigEndian.PutUint32(out[8+len(payload):], n)
	return out
}

// Replay 从原始字节重放，是 Store.Replay 的无状态版本。
func Replay(data []byte) ([]Record, error) {
	var out []Record
	for pos := 0; pos < len(data); {
		if len(data)-pos < 8 { // 头部都不完整：半条，丢弃。
			break
		}
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		sum := binary.BigEndian.Uint32(data[pos+4 : pos+8])
		end := pos + 8 + n + 4
		if n < 0 || end > len(data) { // 负载或尾部缺失：半条，丢弃。
			break
		}
		payload := data[pos+8 : pos+8+n]
		tail := binary.BigEndian.Uint32(data[pos+8+n : end])
		if tail != uint32(n) || crc32.ChecksumIEEE(payload) != sum {
			break // 损坏帧：确定性丢弃本帧及之后全部内容。
		}
		var r Record
		if err := json.Unmarshal(payload, &r); err != nil {
			break
		}
		out = append(out, r)
		pos = end
	}
	return out, nil
}
