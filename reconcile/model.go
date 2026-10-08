package reconcile

// AttributeValue 是副本快照中单个属性的取值及其最后写入的逻辑位点。
type AttributeValue struct {
	Value     string
	WrittenAt uint64
}

// ObjectEntry 是快照中一个对象实例的状态。
type ObjectEntry struct {
	ObjectID   string
	Attributes map[string]AttributeValue
}

// Snapshot 是单个副本在某个逻辑位点产出的快照。
type Snapshot struct {
	ReplicaID string
	// Priority 是副本自身携带的可比较标识，冲突裁决的唯一依据。
	Priority uint64
	// Position 是快照对应的逻辑位点。
	Position uint64
	Objects  []ObjectEntry
}

// Candidate 是参与某个属性裁决的一份候选取值。
type Candidate struct {
	ReplicaID string `json:"replica_id"`
	Priority  uint64 `json:"priority"`
	Value     string `json:"value"`
	WrittenAt uint64 `json:"written_at"`
}
