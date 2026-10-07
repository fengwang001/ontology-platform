package ontology

import "time"

// OpKind 是被审计的操作类别。
type OpKind string

const (
	OpRegisterTenant     OpKind = "register-tenant"
	OpRegisterObjectType OpKind = "register-object-type"
	OpRegisterInstance   OpKind = "register-instance"
	OpSetGlobalDefault   OpKind = "set-global-default"
	OpSetTenantOverride  OpKind = "set-tenant-override"
	OpRevokeOverride     OpKind = "revoke-tenant-override"
	OpDecide             OpKind = "decide"
)

// DecideInput 是一次访问判定调用的完整输入。
type DecideInput struct {
	Subject    Subject
	Action     Action
	InstanceID string
	Attribute  string
}

// AuditRecord 是一条审计记录，完整保存一次被接受调用的输入、
// 最终输出以及（对判定而言）据以裁决的规则层级依据。
//
// 被拒绝的调用不会进入审计日志：拒绝不得对规则状态或审计记录产生影响。
type AuditRecord struct {
	Seq    uint64
	Op     OpKind
	At     time.Time
	Input  any
	Output any // OpDecide 时为 Decision
}
