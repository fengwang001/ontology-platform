package outbox

// Message 是写入发件箱的业务消息。
type Message struct {
	// ID 为消息的业务幂等标识，由调用方提供。
	ID string
	// Key 为分区/排序键，可为空。
	Key string
	// Body 为消息负载，不可为空。
	Body []byte
}

// stored 是发件箱内部记录。骨架阶段仅声明。
type stored struct{}
