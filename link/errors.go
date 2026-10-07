package link

import "errors"

// 创建可能返回的四类显式失败，调用方可通过 errors.Is 精确区分。
var (
	// ErrObjectInstanceDeleted 表示所引用的对象实例已被逻辑删除或不存在。
	// 该判定优先于基数与重复性判定。
	ErrObjectInstanceDeleted = errors.New("link: referenced object instance is deleted")

	// ErrLinkTypeDirectionNotAllowed 表示链接类型不允许当前两个对象类型
	// 按该方向建立链接。
	ErrLinkTypeDirectionNotAllowed = errors.New("link: link type does not allow this direction between the object types")

	// ErrCardinalityExceeded 表示目标方向基数已满。
	ErrCardinalityExceeded = errors.New("link: cardinality limit for the direction reached")

	// ErrDuplicateLink 表示区分属性组合与现有在库链接完全相同，
	// 判定为重复声明而非新增。
	ErrDuplicateLink = errors.New("link: identical discriminator already exists")

	// ErrLinkNotFound 表示删除目标链接当前不在库（可能从未创建或已被撤销）。
	ErrLinkNotFound = errors.New("link: link not found")

	// ErrUnknownLinkType 表示引用了未登记的链接类型。
	ErrUnknownLinkType = errors.New("link: unknown link type")
)

// DecisionLog 是事后核对所需的判定记录条目。
type DecisionLog struct {
	Seq     int64
	Op      string
	Request CreateRequest
	Reason  string
	Result  DecisionResult
	LinkID  uint64
}
