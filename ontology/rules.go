package ontology

import "fmt"

// Effect 是规则判定结果。
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// RuleVersion 是一条规则的不可变版本快照。
// 任何"修改"都产生新的 RuleVersion；已产生的版本永不改变、永不删除，
// 因此版本 ID 的含义恒定，历史导出固化的依据永远可解析。
type RuleVersion struct {
	ID       string // 全局唯一且含义恒定
	TypeName string // 直接声明该规则的类型
	Subject  string // 执行主体
	Property string
	Effect   Effect
	Content  string // 规则载荷快照（命中时刻的具体内容）
	Seq      uint64 // 创建时的全局序号
}

// ruleKey 定位某类型上针对 (主体, 属性) 的生效规则槽位。
type ruleKey struct {
	typeName string
	subject  string
	property string
}

// ruleStore 保存生效规则索引与全部历史版本。
type ruleStore struct {
	active   map[ruleKey]string     // 槽位 -> 当前生效版本 ID
	versions map[string]RuleVersion // 版本 ID -> 不可变快照（只增不删）
	nextSeq  uint64
}

func newRuleStore() *ruleStore {
	return &ruleStore{
		active:   map[ruleKey]string{},
		versions: map[string]RuleVersion{},
	}
}

// upsert 以新版本覆盖槽位，返回新版本 ID；旧版本保留在 versions 中。
func (s *ruleStore) upsert(typeName, subject, property string, effect Effect, content string, seq uint64) string {
	s.nextSeq++
	id := fmt.Sprintf("rv-%d", s.nextSeq)
	s.versions[id] = RuleVersion{
		ID:       id,
		TypeName: typeName,
		Subject:  subject,
		Property: property,
		Effect:   effect,
		Content:  content,
		Seq:      seq,
	}
	s.active[ruleKey{typeName, subject, property}] = id
	return id
}

// delete 移除槽位的生效规则（历史版本保留，继承查找自然回落到祖先规则）。
func (s *ruleStore) delete(typeName, subject, property string) {
	delete(s.active, ruleKey{typeName, subject, property})
}

// resolve 沿继承链（近端在前）查找 (主体, 属性) 命中的生效规则版本。
func (s *ruleStore) resolve(chain []string, subject, property string) (RuleVersion, bool) {
	for _, tn := range chain {
		if id, ok := s.active[ruleKey{tn, subject, property}]; ok {
			v, ok := s.versions[id]
			return v, ok
		}
	}
	return RuleVersion{}, false
}

// version 按 ID 直接取出历史版本快照。
// 单次哈希表查找，O(1)，检查的记录数恒为 1，
// 与该类型体系的历史规则变更总次数无关。
func (s *ruleStore) version(id string) (RuleVersion, bool) {
	v, ok := s.versions[id]
	return v, ok
}
