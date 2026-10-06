package endpointshard

import "errors"

// Kind 是可区分的错误类别。
//
// 优先级从高到低：参数非法 > 服务不存在 > 服务已存在。
// 同一操作命中多个错误条件时，只报告优先级最高者；
// 实现上通过固定的检查顺序保证：先校验参数，再校验存在性，最后校验冲突。
type Kind int

const (
	// KindInvalidArgument 参数非法（优先级最高）。
	KindInvalidArgument Kind = iota + 1
	// KindServiceNotFound 服务不存在。
	KindServiceNotFound
	// KindServiceAlreadyExists 服务已存在（优先级最低）。
	KindServiceAlreadyExists
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid argument"
	case KindServiceNotFound:
		return "service not found"
	case KindServiceAlreadyExists:
		return "service already exists"
	default:
		return "unknown error"
	}
}

// Error 是维护器返回的唯一错误类型，携带可区分的类别。
type Error struct {
	Kind    Kind
	Op      string
	Service string
	Detail  string
}

func (e *Error) Error() string {
	return e.Op + ": service " + e.Service + ": " + e.Kind.String() + ": " + e.Detail
}

// KindOf 从 err 中取出错误类别；ok 为 false 表示不是本包的错误。
func KindOf(err error) (kind Kind, ok bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

func invalidArg(op, service, detail string) *Error {
	return &Error{Kind: KindInvalidArgument, Op: op, Service: service, Detail: detail}
}

func notFound(op, service string) *Error {
	return &Error{Kind: KindServiceNotFound, Op: op, Service: service, Detail: "service does not exist"}
}

func alreadyExists(op, service string) *Error {
	return &Error{Kind: KindServiceAlreadyExists, Op: op, Service: service, Detail: "service already exists"}
}
