// Package fold 负责头部折行（obs-fold）的展开与重新折叠，依赖 token 包。
package fold

import "errors"

var (
	// ErrLeadingContinuation 表示第一个物理行就以空白开头（首行不得是续行）。
	ErrLeadingContinuation = errors.New("fold: first line is a continuation line")
	// ErrEmptyPhysicalLine 表示逻辑行序列中出现了空物理行。
	ErrEmptyPhysicalLine = errors.New("fold: empty physical line inside a field")
	// ErrUnfoldable 表示存在超过行宽且内部无空格可断的 token，硬切会破坏往返。
	ErrUnfoldable = errors.New("fold: token longer than line width cannot be folded")
)
