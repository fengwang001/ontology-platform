// Package ontology 提供带快照与增量编辑回放的存储版本清单（MANIFEST）管理器。
package ontology

import "errors"

// File 表示某一层中的一个文件。
// Level 取 0..6；Num 为正整数；Smallest/Largest 为非空字节串键，
// 按字节序比较且必须满足 Smallest <= Largest。
type File struct {
	Level    int
	Num      uint64
	Smallest []byte
	Largest  []byte
}

// Edit 是一条增量版本编辑。指针字段为 nil 表示该字段未给出。
// Dels 只需给出 Level 与 Num，键区间不参与删除匹配。
type Edit struct {
	Adds      []File
	Dels      []File
	LogNumber *uint64
	NextFile  *uint64
	LastSeq   *uint64
}

// Version 是某个时刻的完整版本。
type Version struct {
	Files     [7][]File
	LogNumber uint64
	NextFile  uint64
	LastSeq   uint64
}

// Record 是清单中的一条记录：Snapshot 或 Edit；Torn 表示记录只写了一半。
type Record struct {
	Snapshot *Version
	Edit     *Edit
	Torn     bool
}

// Disk 是内存模拟的磁盘内容：清单号到记录列表的映射，以及 CURRENT 指针。
// Threshold 记录该磁盘对应管理器的轮转阈值 T，供 Open 使用。
type Disk struct {
	Manifests map[uint64][]Record
	Current   uint64
	Threshold int
}

// 预定义错误。
var (
	ErrParam     = errors.New("ontology: invalid parameter")
	ErrNoFile    = errors.New("ontology: file to delete not found")
	ErrDupFile   = errors.New("ontology: duplicate file number")
	ErrRegress   = errors.New("ontology: file/log/seq number regression")
	ErrLogAhead  = errors.New("ontology: log number not below next file number")
	ErrOverlap   = errors.New("ontology: overlapping files in level")
	ErrNoCurrent = errors.New("ontology: CURRENT manifest missing")
	ErrCorrupt   = errors.New("ontology: corrupt manifest record")
)

// CorruptError 在恢复期间携带记录下标与具体原因。errors.Is 可同时命中
// ErrCorrupt（通过 Is）与其包裹的具体原因（通过 Unwrap 链）。
type CorruptError struct {
	Index int
	Err   error
}

func (e *CorruptError) Error() string { return e.Err.Error() }
func (e *CorruptError) Unwrap() error { return e.Err }

// Is 使 errors.Is(err, ErrCorrupt) 对本类型成立；
// 具体原因（ErrParam/ErrOverlap 等）经 Unwrap 链命中。
func (e *CorruptError) Is(target error) bool { return target == ErrCorrupt }
