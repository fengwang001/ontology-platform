package quota

import "fmt"

// OpKind 批处理操作类型。
type OpKind int

const (
	KindMkdir OpKind = iota
	KindAddFile
	KindResize
	KindRemove
	KindRename
	KindReserve
	KindRelease
	KindSetQuota
)

// Op 批处理中的单个操作，用 OpMkdir 等构造函数创建。
// 操作可引用批内前面新建节点的编号（编号按成功顺序连续分配，调用者可预先推算）。
type Op struct {
	Kind OpKind
	X    int   // Mkdir/AddFile 的父目录；其余操作的目标节点
	P    int   // 仅 Rename：新父目录
	Size int64 // AddFile 的 size、Resize 的 s、Reserve/Release 的 b、SetQuota 的字节限额
	N    int64 // 仅 SetQuota：条目数限额
}

// OpMkdir 构造 Mkdir(p) 操作。
func OpMkdir(p int) Op { return Op{Kind: KindMkdir, X: p} }

// OpAddFile 构造 AddFile(p, size) 操作。
func OpAddFile(p int, size int64) Op { return Op{Kind: KindAddFile, X: p, Size: size} }

// OpResize 构造 Resize(f, s) 操作。
func OpResize(f int, s int64) Op { return Op{Kind: KindResize, X: f, Size: s} }

// OpRemove 构造 Remove(x) 操作。
func OpRemove(x int) Op { return Op{Kind: KindRemove, X: x} }

// OpRename 构造 Rename(x, p) 操作。
func OpRename(x, p int) Op { return Op{Kind: KindRename, X: x, P: p} }

// OpReserve 构造 Reserve(d, b) 操作。
func OpReserve(d int, b int64) Op { return Op{Kind: KindReserve, X: d, Size: b} }

// OpRelease 构造 Release(d, b) 操作。
func OpRelease(d int, b int64) Op { return Op{Kind: KindRelease, X: d, Size: b} }

// OpSetQuota 构造 SetQuota(d, b, n) 操作。
func OpSetQuota(d int, b, n int64) Op { return Op{Kind: KindSetQuota, X: d, Size: b, N: n} }

// String 描述操作内容，用于日志。
func (op Op) String() string {
	switch op.Kind {
	case KindMkdir:
		return fmt.Sprintf("Mkdir(%d)", op.X)
	case KindAddFile:
		return fmt.Sprintf("AddFile(%d, %d)", op.X, op.Size)
	case KindResize:
		return fmt.Sprintf("Resize(%d, %d)", op.X, op.Size)
	case KindRemove:
		return fmt.Sprintf("Remove(%d)", op.X)
	case KindRename:
		return fmt.Sprintf("Rename(%d, %d)", op.X, op.P)
	case KindReserve:
		return fmt.Sprintf("Reserve(%d, %d)", op.X, op.Size)
	case KindRelease:
		return fmt.Sprintf("Release(%d, %d)", op.X, op.Size)
	case KindSetQuota:
		return fmt.Sprintf("SetQuota(%d, %d, %d)", op.X, op.Size, op.N)
	}
	return fmt.Sprintf("Op(?%d)", op.Kind)
}

func (s *state) apply(op Op) error {
	var err error
	switch op.Kind {
	case KindMkdir:
		_, err = s.mkdir(op.X)
	case KindAddFile:
		_, err = s.addFile(op.X, op.Size)
	case KindResize:
		err = s.resize(op.X, op.Size)
	case KindRemove:
		err = s.remove(op.X)
	case KindRename:
		err = s.rename(op.X, op.P)
	case KindReserve:
		err = s.reserve(op.X, op.Size)
	case KindRelease:
		err = s.release(op.X, op.Size)
	case KindSetQuota:
		err = s.setQuota(op.X, op.Size, op.N)
	default:
		err = invalidArgErr("unknown op kind %d", op.Kind)
	}
	return err
}

// Batch 按序原子地执行一组操作：任一操作被拒绝则整批回滚
// （用量、预留、限额、编号全部恢复），返回首个失败操作的下标与原因。
// 失败时返回的错误同时可用 errors.Is 判出 ErrBatchFailed 与底层原因。
// 成功时返回 (-1, nil)。ops 为空报 ErrInvalidArgument。
func (l *Ledger) Batch(ops []Op) (int, error) {
	if len(ops) == 0 {
		return -1, invalidArgErr("empty batch")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ns := l.st.clone()
	for i, op := range ops {
		if err := ns.apply(op); err != nil {
			return i, &BatchError{Index: i, Err: err}
		}
	}
	l.st = ns
	return -1, nil
}
