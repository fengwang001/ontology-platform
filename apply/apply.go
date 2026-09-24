// Package apply 按步骤序列执行重命名，校验每步无覆盖，并把可撤销日志写入本地临时目录。
package apply

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"

	"ontology/name"
	"ontology/plan"
)

// ErrStepClobber 表示某一步执行前目标名已存在（理论上编排正确时不会发生）。
var ErrStepClobber = errors.New("apply: target exists before step (would clobber)")

// 记录标志位。
const (
	flagDone byte = 0
	flagUndo byte = 1
)

var magic = []byte("RNLOG001") // 8 字节头部

// Log 是追加式撤销日志句柄。
type Log struct{ f *os.File }

// CreateLog 在临时目录下新建日志文件并写入头部。
func CreateLog(dir string) (*Log, string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "rename-log-*")
	if err != nil {
		return nil, "", err
	}
	if _, err := f.Write(magic); err != nil {
		f.Close()
		return nil, "", err
	}
	return &Log{f: f}, filepath.Join(dir, f.Name()), nil
}

// File 返回底层文件路径。
func (l *Log) File() string { return l.f.Name() }

// Close 关闭日志。
func (l *Log) Close() error { return l.f.Close() }

// Remove 删除日志文件（演示收尾用）。
func (l *Log) Remove() error { return os.Remove(l.f.Name()) }

// append 追加一条 old→new 记录：2B oldLen + old + 2B newLen + new + 1B flag + 4B CRC。
func (l *Log) append(rec record) error {
	buf := encode(rec)
	_, err := l.f.Write(buf)
	return err
}

type record struct {
	old, new string
	flag     byte
}

func encode(r record) []byte {
	buf := make([]byte, 0, 2+len(r.old)+2+len(r.new)+1+4)
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(r.old)))
	buf = append(buf, lenBuf...)
	buf = append(buf, r.old...)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(r.new)))
	buf = append(buf, lenBuf...)
	buf = append(buf, r.new...)
	buf = append(buf, r.flag)
	crc := crc32.ChecksumIEEE(buf)
	crcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBuf, crc)
	buf = append(buf, crcBuf...)
	return buf
}

// Exec 在持锁状态下执行全部步骤。failAt>0 时第 failAt 步（1 基）返回错误，
// 此前已完成步骤自动逆序撤销，命名空间回到执行前。
func Exec(ns *name.Namespace, steps []plan.Step, log *Log, failAt int) error {
	if !ns.TryLock() {
		return name.ErrLocked
	}
	defer ns.Unlock()

	done := 0
	for i, s := range steps {
		if ns.ContainsLocked(s.New) {
			rollback(ns, log, steps[:done])
			return ErrStepClobber
		}
		if i+1 == failAt {
			rollback(ns, log, steps[:done])
			return errors.New("apply: injected failure at step " + itoa(failAt))
		}
		if err := log.append(record{s.Old, s.New, flagDone}); err != nil {
			rollback(ns, log, steps[:done])
			return err
		}
		if err := log.f.Sync(); err != nil {
			rollback(ns, log, steps[:done])
			return err
		}
		if err := ns.MoveLocked(s.Old, s.New); err != nil {
			rollback(ns, log, steps[:done])
			return err
		}
		done++
	}
	return nil
}

// rollback 逆序撤销 steps，不依赖文件（日志用于崩溃后跨进程撤销）。
func rollback(ns *name.Namespace, log *Log, steps []plan.Step) {
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		_ = ns.UndoMoveLocked(s.Old, s.New)
		_ = log.append(record{s.Old, s.New, flagUndo})
	}
	_ = log.f.Sync()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
