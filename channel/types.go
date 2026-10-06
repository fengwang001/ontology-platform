package channel

// Config 是频道创建时固定的参数。
type Config struct {
	EditWindow   int64 // E：编辑时限（秒），严格小于才可编辑
	RecallWindow int64 // R：撤回时限（秒），严格小于才可撤回
	MaxEdits     int   // K：单条消息最多次编辑数
}

const (
	maxBodyLen    = 4000
	maxMentions   = 20
	maxFetchLimit = 100
	maxNow        = int64(1_000_000_000_000)
)

// Message 是完整消息视图（仅未撤回消息）。
type Message struct {
	Seq       int64
	Author    string
	Body      string
	Mentions  []string
	CreatedAt int64
	EditedAt  int64 // 最近一次成功编辑时刻；未编辑时为 0
	EditCount int
}

// Placeholder 是撤回消息的占位视图，只保留序号与撤回痕迹。
type Placeholder struct {
	Seq        int64
	RecalledBy string
	RecalledAt int64
}

// Item 是 Fetch 返回的统一视图：未撤回时 Message 非 nil，撤回时 Placeholder 非 nil。
type Item struct {
	Seq         int64
	Message     *Message
	Placeholder *Placeholder
}

// member 保存成员身份及其当前加入轮次的读水位。
type member struct {
	admin     bool
	watermark int64
}

// storedMessage 是消息的内部存储：撤回后正文与提及被清除，序号保留。
type storedMessage struct {
	seq        int64
	author     string
	body       string
	mentions   []string
	createdAt  int64
	editedAt   int64
	editCount  int
	recalled   bool
	recalledBy string
	recalledAt int64
}
