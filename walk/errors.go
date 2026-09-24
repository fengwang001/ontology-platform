package walk

import (
	"errors"
	"fmt"
)

// nodeMissingError 包装 ErrNodeMissing 并指名缺失节点。
type nodeMissingError struct{ Node string }

func (e *nodeMissingError) Error() string {
	return fmt.Sprintf("%s: %q", ErrNodeMissing, e.Node)
}

func (e *nodeMissingError) Unwrap() error { return ErrNodeMissing }

func missingError(id string) error { return &nodeMissingError{Node: id} }

// MissingNode 从错误中取出被指名的缺失节点；无则返回空串。
func MissingNode(err error) string {
	var e *nodeMissingError
	if errors.As(err, &e) {
		return e.Node
	}
	return ""
}
