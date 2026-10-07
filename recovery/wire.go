package recovery

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
)

// 帧格式（大端）：
//
//	快照文件: magic "ONTS\n" | be32(epoch) be64(seq)
//	          对每个对象重复一帧:
//	              'R'
//	              be32(headerLen) | header | be32(headerCRC32)
//	              be32(payloadLen) | payload | be32(payloadCRC32)
//	          'E'
//
//	动作日志: magic "ONTL\n" | be32(startEpoch) be64(startSeq)
//	          对每条动作重复一帧:
//	              'A' be32(payloadLen) | payload | be32(payloadCRC32)
//	          'E'
//
// 对象帧的 header 只含 {"id":...} 且 CRC 独立存放，使“ID 可读而负载
// 损坏”可以归因到具体对象（对象级不可读）；header CRC 自身损坏则无法归因，
// 属于定界级硬错误。payload CRC 仅覆盖负载本身。

const (
	snapMagic = "ONTS\n"
	logMagic  = "ONTL\n"
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func checksum(b []byte) uint32 { return crc32.Checksum(b, crcTable) }

func appendU32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

func appendU64(b []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(b, buf[:]...)
}

func readU32(b []byte) (uint32, []byte, error) {
	if len(b) < 4 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return binary.BigEndian.Uint32(b[:4]), b[4:], nil
}

func readU64(b []byte) (uint64, []byte, error) {
	if len(b) < 8 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return binary.BigEndian.Uint64(b[:8]), b[8:], nil
}

func readLenPrefixed(b []byte) ([]byte, []byte, error) {
	n, rest, err := readU32(b)
	if err != nil {
		return nil, nil, err
	}
	if uint32(len(rest)) < n {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return rest[:n], rest[n:], nil
}

type objectHeader struct {
	ID ObjectID `json:"id"`
}

type objectPayload struct {
	Exists bool  `json:"exists"`
	State  State `json:"state"`
}

type actionPayload struct {
	Seq        int                         `json:"seq"`
	Epoch      int                         `json:"epoch"`
	VersionSeq int64                       `json:"version_seq"`
	Effects    map[string]objectEffectWire `json:"effects"`
}

type objectEffectWire struct {
	HasStart bool  `json:"has_start"`
	Start    State `json:"start,omitempty"`
	Change   State `json:"change"`
}

// EncodeSnapshot 将逻辑快照编码为带逐条校验的快照字节流。
func EncodeSnapshot(s Snapshot) ([]byte, error) {
	seen := map[ObjectID]bool{}
	var b bytes.Buffer
	b.WriteString(snapMagic)
	b.Write(appendU32(nil, uint32(s.BaseVersion.Epoch)))
	b.Write(appendU64(nil, uint64(s.BaseVersion.Seq)))
	for _, obj := range s.Objects {
		if seen[obj.ID] {
			return nil, fmt.Errorf("duplicate object id in snapshot: %q", obj.ID)
		}
		seen[obj.ID] = true
		header, err := json.Marshal(objectHeader{ID: obj.ID})
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(objectPayload{Exists: obj.Exists, State: obj.State})
		if err != nil {
			return nil, err
		}
		b.WriteByte('R')
		b.Write(appendU32(nil, uint32(len(header))))
		b.Write(header)
		b.Write(appendU32(nil, checksum(header)))
		b.Write(appendU32(nil, uint32(len(payload))))
		b.Write(payload)
		b.Write(appendU32(nil, checksum(payload)))
	}
	b.WriteByte('E')
	return b.Bytes(), nil
}

// DecodeSnapshot 解析快照字节流，逐条校验。
//
// snap 只包含完好记录，BaseVersion 始终填充；corrupt 按对象 ID 记录
// “ID 可读而负载不可读”的损坏，语义为快照时刻状态不可读，与对象不存在
// 严格区分；err 只用于帧定界或对象头损坏这类无法归因到对象的硬错误。
func DecodeSnapshot(data []byte) (snap Snapshot, corrupt map[ObjectID]string, err error) {
	corrupt = map[ObjectID]string{}
	if len(data) < len(snapMagic)+4+8 || string(data[:len(snapMagic)]) != snapMagic {
		return Snapshot{}, corrupt, fmt.Errorf("bad snapshot magic or truncated preamble")
	}
	rest := data[len(snapMagic):]
	epoch, rest, err := readU32(rest)
	if err != nil {
		return Snapshot{}, corrupt, err
	}
	seq, rest, err := readU64(rest)
	if err != nil {
		return Snapshot{}, corrupt, err
	}
	snap.BaseVersion = Version{Epoch: int(epoch), Seq: int64(seq)}

	for len(rest) > 0 {
		marker := rest[0]
		rest = rest[1:]
		if marker == 'E' {
			if len(rest) != 0 {
				return snap, corrupt, fmt.Errorf("trailing bytes after snapshot terminator")
			}
			return snap, corrupt, nil
		}
		if marker != 'R' {
			return snap, corrupt, fmt.Errorf("invalid snapshot frame marker %q", marker)
		}
		headerBytes, r, err := readLenPrefixed(rest)
		if err != nil {
			return snap, corrupt, fmt.Errorf("snapshot frame header length: %w", err)
		}
		headerCRC, r, err := readU32(r)
		if err != nil {
			return snap, corrupt, err
		}
		payloadLen, r, err := readU32(r)
		if err != nil {
			return snap, corrupt, err
		}
		if uint32(len(r)) < payloadLen+4 {
			return snap, corrupt, io.ErrUnexpectedEOF
		}
		payloadBytes := r[:payloadLen]
		payloadCRC := binary.BigEndian.Uint32(r[payloadLen : payloadLen+4])
		rest = r[payloadLen+4:]

		if checksum(headerBytes) != headerCRC {
			return snap, corrupt, &Error{
				Kind:    KindSnapshotRecordCorrupt,
				Message: "snapshot object header checksum mismatch (object unidentifiable)",
			}
		}
		var header objectHeader
		if err := json.Unmarshal(headerBytes, &header); err != nil || header.ID == "" {
			return snap, corrupt, &Error{
				Kind:    KindSnapshotRecordCorrupt,
				Message: "snapshot object header unparseable",
			}
		}
		if _, dup := corrupt[header.ID]; dup {
			return snap, corrupt, fmt.Errorf("duplicate object id in snapshot: %q", header.ID)
		}
		for _, obj := range snap.Objects {
			if obj.ID == header.ID {
				return snap, corrupt, fmt.Errorf("duplicate object id in snapshot: %q", header.ID)
			}
		}
		if checksum(payloadBytes) != payloadCRC {
			corrupt[header.ID] = "payload checksum mismatch"
			continue
		}
		var payload objectPayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			corrupt[header.ID] = "payload unparseable: " + err.Error()
			continue
		}
		snap.Objects = append(snap.Objects, ObjectSnapshot{
			ID: header.ID, Exists: payload.Exists, State: payload.State,
		})
	}
	return snap, corrupt, fmt.Errorf("snapshot stream truncated before terminator")
}

func startValue(eff ObjectEffect) State {
	if eff.Start == nil {
		return ""
	}
	return *eff.Start
}

func encodeActionFrame(a Action) ([]byte, error) {
	if a.Seq < 0 {
		return nil, fmt.Errorf("negative action seq: %d", a.Seq)
	}
	effects := map[string]objectEffectWire{}
	for id, eff := range a.Effects {
		if id == "" {
			return nil, fmt.Errorf("empty object id in action seq %d", a.Seq)
		}
		effects[string(id)] = objectEffectWire{
			HasStart: eff.Start != nil,
			Start:    startValue(eff),
			Change:   eff.Change,
		}
	}
	payload, err := json.Marshal(actionPayload{
		Seq:        a.Seq,
		Epoch:      a.Version.Epoch,
		VersionSeq: a.Version.Seq,
		Effects:    effects,
	})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteByte('A')
	b.Write(appendU32(nil, uint32(len(payload))))
	b.Write(payload)
	b.Write(appendU32(nil, checksum(payload)))
	return b.Bytes(), nil
}

// EncodeAction 将一条动作编码为带校验的单条记录帧（含帧标记，可直接拼入日志）。
func EncodeAction(a Action) ([]byte, error) {
	return encodeActionFrame(a)
}

func readChecksummedPayload(b []byte) (payload, rest []byte, err error) {
	n, rest, err := readU32(b)
	if err != nil {
		return nil, nil, fmt.Errorf("action frame length: %w", err)
	}
	if uint32(len(rest)) < n+4 {
		return nil, nil, io.ErrUnexpectedEOF
	}
	payload = rest[:n]
	crc := binary.BigEndian.Uint32(rest[n : n+4])
	if checksum(payload) != crc {
		return nil, nil, fmt.Errorf("action payload checksum mismatch")
	}
	return payload, rest[n+4:], nil
}

func parseActionPayload(payload []byte) (Action, error) {
	var wire actionPayload
	if err := json.Unmarshal(payload, &wire); err != nil {
		return Action{}, fmt.Errorf("action payload unparseable: %w", err)
	}
	if wire.Seq < 0 {
		return Action{}, fmt.Errorf("negative action seq")
	}
	a := Action{
		Seq:     wire.Seq,
		Version: Version{Epoch: wire.Epoch, Seq: wire.VersionSeq},
		Effects: map[ObjectID]ObjectEffect{},
	}
	for id, eff := range wire.Effects {
		var start *State
		if eff.HasStart {
			s := eff.Start
			start = &s
		}
		a.Effects[ObjectID(id)] = ObjectEffect{Start: start, Change: eff.Change}
	}
	return a, nil
}

// DecodeAction 解析单条动作记录帧；记录损坏时返回 KindActionRecordCorrupt，
// 不返回任何可部分采信的字段。输入可带或不带前导帧标记 'A'。
func DecodeAction(record []byte, index int) (Action, error) {
	b := record
	if len(b) > 0 && b[0] == 'A' {
		b = b[1:]
	}
	payload, _, err := readChecksummedPayload(b)
	if err != nil {
		return Action{}, &Error{Kind: KindActionRecordCorrupt, RecordIndex: index, Message: err.Error()}
	}
	a, err := parseActionPayload(payload)
	if err != nil {
		return Action{}, &Error{Kind: KindActionRecordCorrupt, RecordIndex: index, Message: err.Error()}
	}
	return a, nil
}

// EncodeLog 将动作序列编码为动作日志字节流（记录帧顺序拼接）。
func EncodeLog(start Version, actions []Action) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(logMagic)
	b.Write(appendU32(nil, uint32(start.Epoch)))
	b.Write(appendU64(nil, uint64(start.Seq)))
	for _, a := range actions {
		frame, err := encodeActionFrame(a)
		if err != nil {
			return nil, err
		}
		b.Write(frame)
	}
	b.WriteByte('E')
	return b.Bytes(), nil
}

// DecodeLog 解析动作日志字节流。
//
// base 为日志声明的起点版本；actions 与 corruptErrs 按记录全局序号对齐，
// 损坏的槽位 actions[i] 为零值、corruptErrs[i] 为 KindActionRecordCorrupt。
// “记录损坏”（整条不可采信）与“记录完整但因原子性被放弃”是两层概念，
// 后者由协调器在重放阶段判定，不在此处处理。
// 若损坏波及帧长度前缀导致帧流失步，返回非 *Error 的定界硬错误。
func DecodeLog(data []byte) (base Version, actions []Action, corruptErrs []error, err error) {
	if len(data) < len(logMagic)+4+8 || string(data[:len(logMagic)]) != logMagic {
		return Version{}, nil, nil, fmt.Errorf("bad action-log magic or truncated preamble")
	}
	rest := data[len(logMagic):]
	epoch, rest, err := readU32(rest)
	if err != nil {
		return Version{}, nil, nil, err
	}
	seq, rest, err := readU64(rest)
	if err != nil {
		return Version{}, nil, nil, err
	}
	base = Version{Epoch: int(epoch), Seq: int64(seq)}

	index := 0
	for len(rest) > 0 {
		marker := rest[0]
		rest = rest[1:]
		if marker == 'E' {
			if len(rest) != 0 {
				return base, actions, corruptErrs, fmt.Errorf("trailing bytes after log terminator")
			}
			return base, actions, corruptErrs, nil
		}
		if marker != 'A' {
			return base, actions, corruptErrs, fmt.Errorf("invalid action frame marker %q", marker)
		}
		n, r, err := readU32(rest)
		if err != nil {
			return base, actions, corruptErrs, fmt.Errorf("action frame %d length prefix unreadable: %w", index, err)
		}
		if uint32(len(r)) < n+4 {
			return base, actions, corruptErrs, fmt.Errorf("action frame %d truncated", index)
		}
		payload := r[:n]
		crc := binary.BigEndian.Uint32(r[n : n+4])
		rest = r[n+4:]
		if checksum(payload) != crc {
			actions = append(actions, Action{})
			corruptErrs = append(corruptErrs, &Error{
				Kind:        KindActionRecordCorrupt,
				RecordIndex: index,
				Message:     "payload checksum mismatch",
			})
			index++
			continue
		}
		a, perr := parseActionPayload(payload)
		if perr != nil {
			actions = append(actions, Action{})
			corruptErrs = append(corruptErrs, &Error{
				Kind:        KindActionRecordCorrupt,
				RecordIndex: index,
				Message:     perr.Error(),
			})
			index++
			continue
		}
		actions = append(actions, a)
		corruptErrs = append(corruptErrs, nil)
		index++
	}
	return base, actions, corruptErrs, fmt.Errorf("action-log stream truncated before terminator")
}
