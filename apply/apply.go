// Package apply 编排整批重命名的执行，写撤销日志，失败时自动逆序回滚。
package apply

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"

	"ontology/name"
	"ontology/plan"
)

const (
	// HeaderMagic 与 TrailerMagic 是日志头/尾魔数，供 undo 解析复用。
	HeaderMagic  = "RNLG1"
	TrailerMagic = "ENDLOG1"
	// HeaderLen 为 头部魔数5 + 步数4 + CRC4。
	HeaderLen = 13
	// TrailerLen 为 CRC4 + 尾部魔数6。
	TrailerLen = 10
)

// FailAt 注入故障：执行第 k 个步骤（1 起）时报错；<=0 表示不注入。
type FailAt int

type record struct {
	From string `json:"from"`
	To   string `json:"to"`
	Ord  int    `json:"ord"`
}

// encodeRecord 生成 0x01 + 大端uint16长度 + JSON 的帧。
func encodeRecord(r record) ([]byte, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 3+len(payload))
	frame[0] = 0x01
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(payload)))
	copy(frame[3:], payload)
	return frame, nil
}

// WriteLog 把步骤编码为完整日志字节（供 undo 与截断测试使用）。
func WriteLog(steps []plan.Step) ([]byte, error) {
	header := make([]byte, HeaderLen)
	copy(header[0:5], HeaderMagic)
	binary.BigEndian.PutUint32(header[5:9], uint32(len(steps)))
	var frames []byte
	for i, s := range steps {
		frame, err := encodeRecord(record{From: s.From, To: s.To, Ord: i + 1})
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame...)
	}
	sum := crc32.ChecksumIEEE(append(header[:9], frames...))
	binary.BigEndian.PutUint32(header[9:13], sum)
	trailer := make([]byte, TrailerLen)
	binary.BigEndian.PutUint32(trailer[0:4], sum)
	copy(trailer[4:10], TrailerMagic)
	return append(append(header, frames...), trailer...), nil
}

// Exec 持写锁执行整批：编译、逐步改名并落日志。
// 注入失败时逆序撤销已执行步、恢复命名空间并删除日志文件。
func Exec(ns *name.Namespace, reqs []plan.Req, dir string, fail FailAt) (string, []plan.Step, error) {
	ns.Lock()
	defer ns.Unlock()
	steps, _, err := plan.Compile(ns, reqs)
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(dir, "rename-log-*.log")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	rollback := func(done int) {
		for i := done - 1; i >= 0; i-- {
			_ = ns.RenameLocked(steps[i].To, steps[i].From, true)
		}
		_ = f.Close()
		_ = os.Remove(path)
	}
	for i, s := range steps {
		if int(fail) == i+1 {
			rollback(i)
			return "", nil, errors.New("apply: injected failure at step " + strconv.Itoa(i+1))
		}
		if err := ns.RenameLocked(s.From, s.To, false); err != nil {
			rollback(i)
			return "", nil, err
		}
		if _, err := encodeRecord(record{From: s.From, To: s.To, Ord: i + 1}); err != nil {
			rollback(i + 1)
			return "", nil, err
		}
	}
	logBytes, err := WriteLog(steps)
	if err != nil {
		rollback(len(steps))
		return "", nil, err
	}
	if _, err := f.Write(logBytes); err != nil {
		rollback(len(steps))
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		rollback(len(steps))
		return "", nil, err
	}
	return filepath.Clean(path), steps, nil
}
