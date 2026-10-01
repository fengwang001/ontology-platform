package pubsub

import "errors"

var (
	// ErrInvalidFilter 表示过滤器非法：空串、"#" 不在末层、通配符与其他字符同层。
	ErrInvalidFilter = errors.New("invalid filter")
	// ErrInvalidTopic 表示发布主题非法：空串或包含通配字符 '+' / '#'。
	ErrInvalidTopic = errors.New("invalid topic")
	// ErrInvalidQoS 表示订阅等级不在 0..2 范围内。
	ErrInvalidQoS = errors.New("invalid qos, must be 0..2")
	// ErrNoSubscription 表示退订了不存在的（客户端, 过滤器）订阅。
	ErrNoSubscription = errors.New("no such subscription")
)
