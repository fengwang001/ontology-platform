package hlc

// EventKind 标识事件类型。
type EventKind int

const (
	KindLocal EventKind = iota
	KindSend
	KindReceive
)

// Event 是历史中的一条记录。
type Event struct {
	Node     string
	Kind     EventKind
	Physical int64
	Stamp    Timestamp
	Message  string
}

// Network 管理一组节点的 HLC 时钟、在途消息与事件历史。
type Network struct {
	nodes     map[string]*nodeState
	messages  map[string]*message
	maxOffset int64
	maxCount  uint32
}

type nodeState struct{}

type message struct{}

// Config 是 Network 的配置。
type Config struct {
	MaxOffset  int64
	MaxCounter uint32
}

// NewNetwork 创建空网络。
func NewNetwork(cfg Config) *Network { return nil }

// AddNode 注册一个节点。
func (n *Network) AddNode(id string) error { return nil }

// Local 为节点的本地事件分配时间戳。
func (n *Network) Local(nodeID string, physical int64) (Timestamp, error) {
	return Timestamp{}, nil
}

// Send 为发送事件分配时间戳，并产生一条在途消息。
func (n *Network) Send(from, to string, physical int64, messageID string) (Timestamp, error) {
	return Timestamp{}, nil
}
// Receive 接收消息并为接收事件分配时间戳。
func (n *Network) Receive(nodeID, messageID string, physical int64) (Timestamp, error) {
	return Timestamp{}, nil
}

// History 返回节点按时间戳有序的事件历史。
func (n *Network) History(nodeID string) ([]Event, error) { return nil, nil }

// Now 返回节点当前时间戳（不产生事件）。
func (n *Network) Now(nodeID string) (Timestamp, error) { return Timestamp{}, nil }
