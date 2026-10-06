package ftl

import (
	"fmt"
	"io"
)

type flashModel interface {
	Write(lpn int) error
	Read(lpn int) (PhysicalPage, error)
	Discard(lpn int) error
	Stats() Stats
	Blocks() []BlockInfo
	PhysicalPages() [][]PageInfo
}

type Tracer struct {
	model  flashModel
	writer io.Writer
	name   string
	seq    int
}

func NewTracer(name string, model flashModel, writer io.Writer) *Tracer {
	return &Tracer{name: name, model: model, writer: writer}
}

func (t *Tracer) Write(lpn int) error {
	err := t.model.Write(lpn)
	t.seq++
	fmt.Fprintf(t.writer,
		"[%s op=%04d] write lpn=%d result=%v basis=%s\n",
		t.name, t.seq, lpn, errorName(err), t.basis())
	return err
}

func (t *Tracer) Read(lpn int) (PhysicalPage, error) {
	physical, err := t.model.Read(lpn)
	t.seq++
	fmt.Fprintf(t.writer,
		"[%s op=%04d] read lpn=%d result=%v physical={block:%d page:%d} basis=%s\n",
		t.name, t.seq, lpn, errorName(err), physical.Block, physical.Page, t.basis())
	return physical, err
}

func (t *Tracer) Discard(lpn int) error {
	err := t.model.Discard(lpn)
	t.seq++
	fmt.Fprintf(t.writer,
		"[%s op=%04d] discard lpn=%d result=%v basis=%s\n",
		t.name, t.seq, lpn, errorName(err), t.basis())
	return err
}

func (t *Tracer) basis() string {
	stats := t.model.Stats()
	blocks := t.model.Blocks()
	mapped := 0
	for blockID := range blocks {
		mapped += blocks[blockID].ValidPages
	}
	return fmt.Sprintf(
		"free=%d retired=%d mapped=%d logicalWrites=%d physicalPrograms=%d eraseCounts=%v",
		stats.FreeBlocks, stats.RetiredBlocks, mapped,
		stats.LogicalWrites, stats.PhysicalPrograms, stats.EraseCounts)
}

func errorName(err error) string {
	switch err {
	case nil:
		return "ok"
	case ErrInvalidArgument:
		return "invalid_argument"
	case ErrUnwritten:
		return "unwritten"
	case ErrNoSpace:
		return "no_space"
	default:
		return err.Error()
	}
}
