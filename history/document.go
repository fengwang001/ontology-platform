package history

// docStatus 是文档的生命周期状态。
type docStatus int

const (
	statusActive   docStatus = iota // 当前正在使用
	statusCached                    // 在前进后退缓存中
	statusUnloaded                  // 已卸载，不可恢复
)

func (s docStatus) String() string {
	switch s {
	case statusActive:
		return "active"
	case statusCached:
		return "cached"
	case statusUnloaded:
		return "unloaded"
	}
	return "unknown"
}

// docFlags 是判定缓存资格的四个条件，全部为 false 时才可缓存。
type docFlags struct {
	networkPending    bool // 有未完成的网络事务
	unloadBlocker     bool // 注册了卸载阻止回调
	exclusiveResource bool // 持有独占资源
	markedUncacheable bool // 被标记为不可缓存
}

func (f docFlags) eligible() bool {
	return !f.networkPending && !f.unloadBlocker && !f.exclusiveResource && !f.markedUncacheable
}

// document 是内核内部文档记录；卸载后记录保留，以便区分"不存在"与"已卸载"。
type document struct {
	id        string
	status    docStatus
	flags     docFlags
	enteredAt int64  // 进入缓存时刻
	order     uint64 // 进入缓存的全局序号，用于同时刻的淘汰仲裁
	heapIndex int    // 在缓存堆中的位置
}
