package ontology

import "time"

// ChangeEvent 是一条时间类属性写入变更事件。
// 事件在写入时刻即锚定当时的默认时区定义版本（AnchoredTzVersion），
// 同时锚定该版本对应的时区 ID（AnchoredZoneID），使事件自包含：
// 视图无需等待迁移记录到达即可解释该事件，分组归属与到达顺序无关。
type ChangeEvent struct {
	ObjectID          string
	ObjectTypeID      string
	WriteSeq          uint64    // 全局逻辑写入序号，同一对象上 last-write-wins 的依据
	LocalValue        time.Time // 写入时刻的本地墙钟原始值
	AnchoredTzVersion int       // 写入时刻生效的默认时区定义版本；0 表示写入时刻尚未定义
	AnchoredZoneID    string    // 写入时刻生效版本的时区 ID；版本为 0 时为空
}

// DecisionRecord 记录一次判定过程的输入、所依据的时区版本与结论，供事后核查。
type DecisionRecord struct {
	Seq               int
	Op                string // "assign" / "stale" / "skip-error" / "evict" / "migrate" / "migrate-rejected"
	ViewID            string
	ObjectID          string
	ObjectTypeID      string
	WriteSeq          uint64
	AnchoredTzVersion int
	ZoneID            string
	LocalValue        string
	InstantUnix       int64
	GroupKey          int64
	Err               string
	Note              string
}
