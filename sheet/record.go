package sheet

// cellChange 记录事务内单个单元格的变化。
type cellChange struct {
	key      string
	oldValue int64
	oldSet   bool
	newValue int64
	newSet   bool
}

// txnRecord 是一条历史记录：一个事务对若干单元格的整体修改。
// 事务内所有单元格共享同一个新版本号，因此只需保存一个期望版本。
type txnRecord struct {
	changes []cellChange // 按键升序，保证“第一个不符单元格”确定
	version int64        // 该记录生效时赋予各单元格的版本（撤销/重做时用作期望版本）
}

// keys 返回记录涉及的单元格键（已按升序存储，直接拷贝）。
func (r *txnRecord) keys() []string {
	keys := make([]string, len(r.changes))
	for i, ch := range r.changes {
		keys[i] = ch.key
	}
	return keys
}

// recordStack 是记录栈，栈顶在切片末尾。
type recordStack struct {
	records []txnRecord
}

func (s *recordStack) len() int { return len(s.records) }

func (s *recordStack) top() (txnRecord, bool) {
	if len(s.records) == 0 {
		return txnRecord{}, false
	}
	return s.records[len(s.records)-1], true
}

func (s *recordStack) push(r txnRecord) { s.records = append(s.records, r) }

func (s *recordStack) pop() txnRecord {
	r := s.records[len(s.records)-1]
	s.records = s.records[:len(s.records)-1]
	return r
}

// trimOldest 丢弃最旧记录直到条数不超过 limit。
func (s *recordStack) trimOldest(limit int) {
	if len(s.records) > limit {
		s.records = append([]txnRecord(nil), s.records[len(s.records)-limit:]...)
	}
}

func (s *recordStack) clear() { s.records = nil }
