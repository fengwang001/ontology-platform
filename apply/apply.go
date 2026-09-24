// Package apply 持锁执行批量重命名、故障自动回滚，并写撤销日志。
package apply

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/name"
	"ontology/plan"
)

// 日志格式：16 字节头 + 记录区 + 4 字节 CRC32（覆盖头与记录区）。
const (
	Magic      = "ONRL"
	Version    = 1
	HeaderSize = 16
	FooterSize = 4
)

// ErrInjected 是故障注入导致的执行失败。
var ErrInjected = errors.New("apply: injected step failure")

// Encode 把步骤序列编码为日志字节。
func Encode(steps []plan.Step) []byte {
	bodyLen := 0
	for _, st := range steps {
		bodyLen += 4 + len(st.Old) + len(st.New)
	}
	out := make([]byte, 0, HeaderSize+bodyLen+FooterSize)
	head := make([]byte, HeaderSize)
	copy(head, Magic)
	head[4] = Version
	binary.BigEndian.PutUint32(head[8:], uint32(len(steps)))
	binary.BigEndian.PutUint32(head[12:], uint32(bodyLen))
	out = append(out, head...)
	for _, st := range steps {
		var ln [4]byte
		binary.BigEndian.PutUint16(ln[0:], uint16(len(st.Old)))
		binary.BigEndian.PutUint16(ln[2:], uint16(len(st.New)))
		out = append(out, ln[:]...)
		out = append(out, st.Old...)
		out = append(out, st.New...)
	}
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(out))
	return append(out, crc[:]...)
}

// WriteLog 把撤销日志写入本地路径。
func WriteLog(path string, steps []plan.Step) error {
	return os.WriteFile(path, Encode(steps), 0o600)
}

// Executor 执行一批已编排的步骤。
type Executor struct {
	LogPath string      // 非空则执行成功后写撤销日志
	FailAt  int         // >=0 时在第 FailAt 步注入失败
	Hook    func(i int) // 每步执行前回调（持锁状态），供测试与演示
}

// Do 持命名空间锁执行整批步骤；期间并发修改被阻塞。任一步失败
// （含注入失败）时，已执行步骤就地逆序回滚，命名空间恢复原状。
func (e *Executor) Do(s *name.Space, steps []plan.Step) error {
	s.Lock()
	defer s.Unlock()
	done := make([]plan.Step, 0, len(steps))
	for i, st := range steps {
		if e.Hook != nil {
			e.Hook(i)
		}
		if i == e.FailAt {
			rollback(s, done)
			return fmt.Errorf("%w: step %d", ErrInjected, i)
		}
		if err := s.RenameLocked(st.Old, st.New); err != nil {
			rollback(s, done)
			return fmt.Errorf("apply: step %d: %w", i, err)
		}
		done = append(done, st)
	}
	if e.LogPath != "" {
		return WriteLog(e.LogPath, steps)
	}
	return nil
}

// rollback 逆序撤销已执行的步骤，调用方须已持锁。
func rollback(s *name.Space, done []plan.Step) {
	for i := len(done) - 1; i >= 0; i-- {
		_ = s.RenameLocked(done[i].New, done[i].Old)
	}
}
