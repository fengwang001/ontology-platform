package cookie

import "errors"

// 五类可区分的拒绝错误。拒绝次序：
// 参数非法 > 时钟回退 > 仅主机前缀约定不符 > 安全来源不符 > 同站无模式缺安全。
var (
	// ErrInvalidArgument 空名字、空站点、路径不以斜杠开头、未知同站模式等。
	ErrInvalidArgument = errors.New("cookie: invalid argument")
	// ErrClockRegression 操作携带的时刻早于存储当前时刻，或时钟被向后拨动。
	ErrClockRegression = errors.New("cookie: clock regression")
	// ErrHostPrefixViolation __Host- 前缀条目未满足 仅安全+根路径+站点一致。
	ErrHostPrefixViolation = errors.New("cookie: __Host- prefix violation")
	// ErrInsecureSource 非安全来源写入仅安全条目。
	ErrInsecureSource = errors.New("cookie: secure cookie from insecure source")
	// ErrSameSiteNoneInsecure 同站模式为无而仅安全为假。
	ErrSameSiteNoneInsecure = errors.New("cookie: SameSite=None requires Secure")
)
