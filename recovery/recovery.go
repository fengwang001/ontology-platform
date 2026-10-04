package recovery

import "errors"

var ErrInvalidArgument = errors.New("recovery: invalid argument")

type Kind int

const (
	KindIndex Kind = iota + 1
	KindDelete
)

type Op struct {
	Seq  int
	ID   string
	Kind Kind
}

type Doc struct {
	ID  string
	Seq int
}

type Mode int

const (
	OpsBased Mode = iota + 1
	FileBased
)

type Plan struct {
	Mode   Mode
	Ops    []Op
	Docs   []Doc
	MaxSeq int
}

type Source interface {
	MaxSeq() int
	H() int
	OpsAfter(c int) []Op
	LiveDocs() []Doc
}

// Plan 只读地为本地检查点 c 的副本选择追赶方式。
// c+1 ≥ H：OpsBased，返回 seq>c 的全部操作（升序、连续无洞）。
// c+1 < H：FileBased，返回当前存活文档（id 字节序）与 maxSeq。
func PlanRecovery(src Source, c int) (Plan, error) {
	if c < 0 || c > src.MaxSeq() {
		return Plan{}, ErrInvalidArgument
	}
	if c+1 >= src.H() {
		return Plan{Mode: OpsBased, Ops: src.OpsAfter(c), MaxSeq: src.MaxSeq()}, nil
	}
	return Plan{Mode: FileBased, Docs: src.LiveDocs(), MaxSeq: src.MaxSeq()}, nil
}
