package ontology

// Rule 是声明在某个对象类型的某个属性上的写权限规则。
// AllowRoles 为空表示任何人不可写；包含 "*" 表示所有人可写。
type Rule struct {
	AllowRoles []string
}

// Allows 判断给定角色是否被该规则允许写入。
func (r Rule) Allows(role string) bool {
	for _, allowed := range r.AllowRoles {
		if allowed == "*" || allowed == role {
			return true
		}
	}
	return false
}

// ObjectType 对象类型：单一父类型 + 各属性上的显式规则声明 + 必需属性。
type ObjectType struct {
	ID       string
	Parent   string // 空串表示根类型
	Rules    map[string]Rule
	Required []string
}

// Instance 对象实例，携带单调递增的版本号。
type Instance struct {
	ID      string
	Type    string
	Version int64
	Props   map[string]any
}
