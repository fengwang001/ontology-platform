package pathlock

// 管理员集合：具备强制释放他人锁的权限。
// 当前采用进程内静态名册；接入真实认证体系时替换本文件即可。
var admins = map[string]struct{}{
	"root":  {},
	"admin": {},
}

// IsAdmin 报告用户是否具备管理员权限。
func IsAdmin(user string) bool {
	_, ok := admins[user]
	return ok
}
