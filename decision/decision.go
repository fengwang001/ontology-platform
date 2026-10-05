// Package decision 维护内容登记与处罚决定，并计算内容的有效级别。
//
// 本包是纯数据登记表：不做参数校验与拒绝次序判定（由门面层负责），
// 只保证「有效级别恒等于全部未推翻决定的 level 最大值」这一不变量。
package decision

// Content 是一条已登记的内容。
type Content struct {
	ID      string
	Creator string
}

// Decision 是一条处罚决定。初始生效，被推翻后 Overturned 为真。
type Decision struct {
	ID         string
	Content    string
	Creator    string // 内容所属创作者（冗余存储，便于计分与申诉鉴权）
	Level      int    // 1=限流 2=限龄 3=下架
	Reviewer   string // 作出决定的审核员
	Now        int64  // 决定时刻
	Overturned bool   // 是否已被申诉复核推翻
	Appealed   bool   // 是否已被申诉过（每条决定至多一次，无论结果）
}

// Store 是内容与决定的登记表。
type Store struct {
	contents  map[string]*Content
	decisions map[string]*Decision
	byContent map[string][]*Decision
}

// NewStore 返回空登记表。
func NewStore() *Store {
	return &Store{
		contents:  make(map[string]*Content),
		decisions: make(map[string]*Decision),
		byContent: make(map[string][]*Decision),
	}
}

// HasContent 报告内容是否已登记。
func (s *Store) HasContent(id string) bool {
	_, ok := s.contents[id]
	return ok
}

// Content 按 id 取内容。
func (s *Store) Content(id string) (*Content, bool) {
	c, ok := s.contents[id]
	return c, ok
}

// AddContent 登记一条新内容（调用方保证 id 不存在）。
func (s *Store) AddContent(id, creator string) {
	s.contents[id] = &Content{ID: id, Creator: creator}
}

// HasDecision 报告决定 id 是否已存在。
func (s *Store) HasDecision(id string) bool {
	_, ok := s.decisions[id]
	return ok
}

// Decision 按 id 取决定。
func (s *Store) Decision(id string) (*Decision, bool) {
	d, ok := s.decisions[id]
	return d, ok
}

// AddDecision 登记一条新决定（调用方保证 id 不存在、内容已登记）。
func (s *Store) AddDecision(d *Decision) {
	s.decisions[d.ID] = d
	s.byContent[d.Content] = append(s.byContent[d.Content], d)
}

// EffectiveLevel 返回内容的有效级别：全部生效（未推翻）决定的 level 最大值，
// 没有生效决定则为 0。有效级别不随计分过期而变化。
func (s *Store) EffectiveLevel(contentID string) int {
	level := 0
	for _, d := range s.byContent[contentID] {
		if !d.Overturned && d.Level > level {
			level = d.Level
		}
	}
	return level
}
