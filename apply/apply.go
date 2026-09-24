// Package apply 加锁执行线性操作序列，写撤销日志，并支持失败自动回滚。
package apply

import (
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"

	"ontology/cycle"
	"ontology/name"
)

// Header 是撤销日志的固定魔数头。
var Header = []byte("RENLOG1\n")

// EncodeRecord 编码一条 old->new 记录：
// uvarint(len)|len(old)|old|len(new)|new|CRC32(载荷)|uvarint(len)。
func EncodeRecord(old, new string) []byte {
	payload := appendUvar(nil, uint64(len(old)))
	payload = append(payload, old...)
	payload = appendUvar(payload, uint64(len(new)))
	payload = append(payload, new...)
	crc := crc32.ChecksumIEEE(payload)
	rec := appendUvar(nil, uint64(len(payload)))
	rec = append(rec, payload...)
	rec = appendUvar(rec, crc)
	rec = appendUvar(rec, uint64(len(payload)))
	return rec
}

func appendUvar(b []byte, v uint64) []byte {
	var buf [10]byte
	n := putUvar(buf[:], v)
	return append(b, buf[:n]...)
}

func putUvar(buf []byte, x uint64) int {
	i := 0
	for x >= 0x80 {
		buf[i] = byte(x) | 0x80
		x >>= 7
		i++
	}
	buf[i] = byte(x)
	return i + 1
}

// Hook 在第 step 步（0 起）实际修改前调用；返回非 nil 即注入失败。
type Hook func(step int, op cycle.Op) error

// Options 控制执行：日志路径、故障注入钩子、每步快照检查器。
type Options struct {
	LogPath string
	FailAt  Hook
	// Check 在持锁状态下、每步修改前调用，可断言“目标名当前不存在”。
	Check func(step int, op cycle.Op, ns *name.Namespace) error
}

// ErrExec 表示执行中途失败（已自动回滚）。
var ErrExec = errors.New("apply: execution failed and rolled back")

// Execute 持命名空间写锁，按序执行 ops；任一步失败则逆序回滚。
// 成功后日志文件保留在 opts.LogPath，供 undo 使用。
func Execute(ns *name.Namespace, ops []cycle.Op, opts Options) (err error) {
	if opts.LogPath == "" {
		opts.LogPath = filepath.Join(os.TempDir(), "rename-undo.log")
	}
	f, err := os.Create(opts.LogPath)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		f.Close()
		if cleanup {
			os.Remove(opts.LogPath)
		}
	}()
	if _, err = f.Write(Header); err != nil {
		return err
	}

	ns.Lock()
	done := 0
	rollback := func() {
		for i := done - 1; i >= 0; i-- {
			op := ops[i]
			_ = renameQuiet(ns, op.New, op.Old) // 逆序撤销
		}
		ns.Unlock()
	}
	for i, op := range ops {
		if opts.Check != nil {
			if e := opts.Check(i, op, ns); e != nil {
				rollback()
				return errors.Join(ErrExec, e)
			}
		}
		if opts.FailAt != nil {
			if e := opts.FailAt(i, op); e != nil {
				rollback()
				return errors.Join(ErrExec, e)
			}
		}
		// 先落日志并刷盘，再修改集合，保证崩溃后仍可撤销。
		if _, e := f.Write(EncodeRecord(op.Old, op.New)); e != nil {
			rollback()
			return errors.Join(ErrExec, e)
		}
		if e := f.Sync(); e != nil {
			rollback()
			return errors.Join(ErrExec, e)
		}
		if e := renameQuiet(ns, op.Old, op.New); e != nil {
			rollback()
			return errors.Join(ErrExec, e)
		}
		done++
	}
	ns.Unlock()
	cleanup = false
	return nil
}

func renameQuiet(ns *name.Namespace, old, new string) error {
	if old == new {
		return nil
	}
	return ns.RenameWhileLocked(old, new)
}
