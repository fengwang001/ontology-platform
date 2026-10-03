package authz

// Permission 是操作者可携带的权限位。
type Permission int

const (
	DeleteVersion Permission = iota
	BypassGovernance
	PutRetention
)

// Operator 为一次调用携带的权限集合。
type Operator map[Permission]bool

// New 构造一个携带给定权限位的操作者；nil 操作者视为无任何权限。
func New(perms ...Permission) Operator {
	o := Operator{}
	for _, p := range perms {
		o[p] = true
	}
	return o
}

// Has 报告操作者是否携带指定权限。
func (o Operator) Has(p Permission) bool { return o[p] }

func (o Operator) CanDeleteVersion() bool    { return o.Has(DeleteVersion) }
func (o Operator) CanBypassGovernance() bool { return o.Has(BypassGovernance) }
func (o Operator) CanPutRetention() bool     { return o.Has(PutRetention) }
