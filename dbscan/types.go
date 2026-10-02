package dbscan

// Point 是一个存活二维整数点。
type Point struct {
	ID   int64
	X    int64
	Y    int64
	Born int64
}

// Change 表示一次操作前后单个点标签的变化。
type Change struct {
	ID       int64
	OldLabel int64
	NewLabel int64
}

// EventType 为簇事件类型。
type EventType string

const (
	EventBirth   EventType = "Birth"
	EventDeath   EventType = "Death"
	EventRelabel EventType = "Relabel"
	EventMerge   EventType = "Merge"
	EventSplit   EventType = "Split"
	EventReshape EventType = "Reshape"
)

// Event 表示一次操作引起的一个簇连通分量事件。
type Event struct {
	Type      EventType
	OldLabels []int64
	NewLabels []int64
}

// Result 是一次被接受操作的返回结果。
type Result struct {
	Changes []Change
	Events  []Event
}

// Cluster 是一个簇标签及其全部成员（不含噪声）。
type Cluster struct {
	Label   int64
	Members []int64
}
