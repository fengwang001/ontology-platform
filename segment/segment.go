// Package segment 在一段字节缓冲（Device）上提供记录的追加与顺序扫描。
// 它依赖 codec 做记录编解码，不依赖 wal。
package segment

import (
	"ontology/codec"
)

// StopReason 表示一次扫描为何停下。
type StopReason int

const (
	// StopEOF 表示完整扫到了扫描上限，一路没有损坏。
	StopEOF StopReason = iota
	// StopTruncated 表示遇到半条记录。
	StopTruncated
	// StopCorrupt 表示遇到校验和不符的记录。
	StopCorrupt
)

// ScanReport 是一次顺序扫描的结论。
type ScanReport struct {
	StopAt int64        // 停下的字节偏移：EOF 时为扫描上限，损坏时为坏记录的起点
	Reason StopReason   // 停下的原因
	Detail codec.Result // 损坏时 codec 给出的可判定细节
}

// Segment 管理一个 Device 上按记录追加与顺序扫描。
// 追加并发安全，记录之间绝不交错。
type Segment struct {
	dev *Device
}

// New 返回一个基于 dev 的 Segment。
func New(dev *Device) *Segment {
	return &Segment{dev: dev}
}

// Append 把 payload 编码成一条记录追加到段尾，返回记录起始偏移。
// 编码与落盘是一次原子追加，不修改任何已写入字节。
func (s *Segment) Append(payload []byte) int64 {
	return s.dev.Append(codec.Encode(payload))
}

// Scan 从 from 开始顺序解码，最多读到 limit（不含）为止。
// 每解出一条完整记录就回调 fn（参数为记录偏移与负载），
// fn 返回 false 时提前结束，报告 StopEOF。
// 遇到半条记录或校验不符立即停下，停止点之后的字节一律不解释。
func (s *Segment) Scan(from, limit int64, fn func(off int64, payload []byte) bool) ScanReport {
	pos := from
	for pos < limit {
		res := codec.Decode(s.dev.Bytes(pos, limit))
		if res.Kind != codec.KindOK {
			return ScanReport{
				StopAt: pos,
				Reason: stopReasonOf(res.Kind),
				Detail: res,
			}
		}
		if !fn(pos, res.Payload) {
			return ScanReport{StopAt: pos + int64(res.Consumed), Reason: StopEOF}
		}
		pos += int64(res.Consumed)
	}
	return ScanReport{StopAt: pos, Reason: StopEOF}
}

func stopReasonOf(k codec.Kind) StopReason {
	if k == codec.KindCorrupt {
		return StopCorrupt
	}
	return StopTruncated
}
