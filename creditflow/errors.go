package creditflow

import "errors"

// 非法输入的错误类别，互不相同、可区分。
var (
	// ErrInvalidParam：非正参数或消费编号越界（非连续/重复）。
	ErrInvalidParam = errors.New("creditflow: invalid parameter")
	// ErrProbeNotAllowed：探测条件不满足（当前有信用或无积压）。
	ErrProbeNotAllowed = errors.New("creditflow: probe not allowed")
	// ErrBacklogOverflow：产生消息会导致积压超过上限。
	ErrBacklogOverflow = errors.New("creditflow: backlog overflow")
)
