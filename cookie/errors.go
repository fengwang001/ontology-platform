package cookie

import "errors"

// 参数非法的细分原因，便于调用方区分五种错误类别中的“参数非法”。
var (
	ErrEmptyName       = &InvalidArgError{"cookie name must not be empty"}
	ErrEmptyDomain     = &InvalidArgError{"domain/site must not be empty"}
	ErrBadPath         = &InvalidArgError{"path must start with '/'"}
	ErrUnknownSameSite = &InvalidArgError{"unknown same-site mode"}
)

// 可区分的五类拒绝错误；InvalidArgError 承载具体的参数非法原因。
var (
	ErrClockRollback        = errors.New("cookie: clock moved backwards")
	ErrInsecureSource       = errors.New("cookie: secure cookie cannot be written from non-secure source")
	ErrHostPrefixViolation  = errors.New("cookie: __Host- prefix requirements not satisfied")
	ErrSameSiteNoneInsecure = errors.New("cookie: SameSite=None requires Secure")
)

// InvalidArgError 表示参数非法类拒绝（空名字、空站点、路径不合法、未知同站模式）。
type InvalidArgError struct{ Reason string }

func (e *InvalidArgError) Error() string { return "cookie: invalid argument: " + e.Reason }

// AsInvalidArg 提取参数非法错误，非参数非法时返回 nil。
func AsInvalidArg(err error) *InvalidArgError {
	var target *InvalidArgError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
