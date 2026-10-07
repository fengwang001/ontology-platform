package ontology

// PathEntry 记录传播路径上的一个节点，是裁决的依据。
type PathEntry struct {
	Instance InstanceID
	Depth    int
	ViaLink  LinkTypeID // 到达该节点所经过的链接类型；直接目标为空
}

// SkipRecord 记录一个因不可见而被跳过的实例及其影响范围。
// 除“存在且不可见”之外不携带该实例的任何属性信息。
type SkipRecord struct {
	Instance InstanceID
	Reason   string
	Impact   string
}

// DecisionRecord 完整记录一次调用的输入、最终输出与传播路径依据。
type DecisionRecord struct {
	Seq        int64
	Subject    SubjectID
	Inv        Invocation
	Path       []PathEntry // 实际遍历到的传播路径（去重后）
	Checked    []InstanceID
	Skipped    []SkipRecord
	CheckCount int
	Allowed    bool
	Err        ErrorKind // 0 表示无错误
}

// DecisionLog 是判定日志。只在 Engine 的串行锁内追加。
type DecisionLog struct {
	Records []DecisionRecord
}

func (l *DecisionLog) append(r DecisionRecord) {
	r.Seq = int64(len(l.Records)) + 1
	l.Records = append(l.Records, r)
}
