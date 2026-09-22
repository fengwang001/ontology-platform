package entry

import "ontology/version"

// State 是单个缓存条目的生命周期状态。
type State uint8

const (
	// Hole：从未回源过，或条目被淘汰后重建，处于"可重新回源"的空洞。
	Hole State = iota
	// Valid：持有与版本绑定的数据（也可能是"不存在"负缓存），未过期。
	Valid
	// Stale：已收到更高版本失效，或已过存活时长，等待重新回源。
	Stale
	// Fetching：回源进行中，可能携带一个旧版本的快照。
	Fetching
)

func (s State) String() string {
	switch s {
	case Valid:
		return "valid"
	case Stale:
		return "stale"
	case Fetching:
		return "fetching"
	default:
		return "hole"
	}
}

// Payload 是回源得到的一份数据。Exists 为 false 表示后端确认键不存在。
type Payload struct {
	Data   []byte
	Exists bool
	Ver    version.Version
}

// Snapshot 是条目的不可变只读视图。
type Snapshot struct {
	State   State
	Exists  bool
	Data    []byte
	DataVer version.Version
	SeenVer version.Version
	Expire  int64
	Fetches uint64
	Invalid uint64
	Loaded  bool
}
