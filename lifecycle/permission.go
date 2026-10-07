package lifecycle

// 权限动作。
const (
	ActionDelete = "delete"
	ActionRevive = "revive"
)

// PermissionEntry 是可被独立吊销的权限条目。
// 审计中只保存其在操作时点的快照引用，事后吊销不影响历史审计可读性。
type PermissionEntry struct {
	ID      string
	Grants  []string // 授予的动作集合，如 ActionDelete / ActionRevive
	Version int64
	Revoked bool
}

// PermissionSnapshot 是权限条目在授权时点的不可变快照（以引用方式落审计）。
type PermissionSnapshot struct {
	PermissionID string
	Version      int64
	Grants       []string
	WasRevoked   bool
	CapturedAt   int64
}
