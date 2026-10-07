package linkrepair

// Validator 负责单条记录的结构有效性判定（裁决优先级第 1 级）。
//
// 一条记录结构有效，当且仅当：
//  1. 链接类型非空且存在于类型注册表（否则无法解析为任何已声明类型的实例）；
//  2. 两端对象标识都非空（任一端丢失或不可解析即为残留记录，
//     只能整体舍弃，禁止用默认值或猜测补全）。
type Validator struct {
	types map[LinkTypeID]LinkType
}

// NewValidator 基于链接类型注册表构造结构校验器。
func NewValidator(types map[LinkTypeID]LinkType) *Validator {
	return &Validator{types: types}
}

// Check 判定一条原始记录能否解析为完整链接。
// 该方法为纯函数，不修改入参。
func (v *Validator) Check(rec RawRecord) (Link, bool) {
	if rec.Type == "" {
		return Link{}, false
	}
	lt, ok := v.types[rec.Type]
	if !ok {
		return Link{}, false
	}
	if !lt.Cardinality.valid() {
		// 类型注册表自身非法：无法据此裁决，按结构不可解析处理。
		return Link{}, false
	}
	if rec.From == "" || rec.To == "" {
		return Link{}, false
	}
	return Link{Type: rec.Type, From: rec.From, To: rec.To}, true
}

func (c Cardinality) valid() bool {
	fromOK := c.MaxFrom == Unbounded || c.MaxFrom >= 1
	toOK := c.MaxTo == Unbounded || c.MaxTo >= 1
	return fromOK && toOK
}
