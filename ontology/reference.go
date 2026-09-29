package ontology

import (
	"context"
)

// Reference 是朴素参照：同样按版本条件写入/删除，但墓碑永不清除。
// 当被测系统中所有被清除墓碑之后都没有更旧版本的迟到写入时，
// 两者的存活行与水位必然一致。
type Reference struct{}

// NewReference 创建朴素参照。
func NewReference() *Reference { return nil }

// Commit 顺序应用一批事件。
func (r *Reference) Commit(ctx context.Context, events []Event) (BatchResult, error) {
	return BatchResult{}, nil
}

// Get 返回存活行。
func (r *Reference) Get(ctx context.Context, key string) (Row, bool) { return Row{}, false }

// List 返回按键升序的存活行。
func (r *Reference) List() []Row { return nil }

// Watermark 返回水位。
func (r *Reference) Watermark() int64 { return 0 }

// IgnoredCount 返回忽略计数。
func (r *Reference) IgnoredCount() int64 { return 0 }
