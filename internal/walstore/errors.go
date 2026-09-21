package walstore

import "errors"

var (
	// ErrEmptyBatch 表示提交了一个空批次。
	ErrEmptyBatch = errors.New("walstore: empty batch")
	// ErrEmptyKey 表示批次中包含空键。
	ErrEmptyKey = errors.New("walstore: empty key")
	// ErrClosed 表示在已关闭的 Store 上操作。
	ErrClosed = errors.New("walstore: store closed")
)

// validateBatch 校验批次合法性；值允许为空串，键不允许。
func validateBatch(batch map[string]string) error {
	if len(batch) == 0 {
		return ErrEmptyBatch
	}
	for k := range batch {
		if k == "" {
			return ErrEmptyKey
		}
	}
	return nil
}
