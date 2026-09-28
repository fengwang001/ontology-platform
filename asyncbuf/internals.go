package asyncbuf

import (
	"fmt"
	"io"
	"strconv"
)

// entry 是缓冲内部的一条输入事件。
type entry struct {
	kind Kind
	id   string
	seq  int64

	// order 为输入序号；seg 为事件所属段（仅无序模式使用）。
	order int
	seg   int

	// done 表示元素已被调用方声明完成；doneOrder 为完成声明的先后序号。
	done      bool
	doneOrder int
}

func (e *entry) desc() string {
	if e.kind == WatermarkEvent {
		return "watermark seq=" + strconv.FormatInt(e.seq, 10)
	}
	return "element id=" + e.id
}

func errText(err error) string { return err.Error() }

func (b *Buffer) logf(format string, args ...any) {
	if b.log == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	mode := "ordered"
	if b.mode == Unordered {
		mode = "unordered"
	}
	_, _ = io.WriteString(b.log, "["+mode+"] "+msg+"\n")
}
