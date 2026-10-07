package ontology

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// CountDirection 与 Direction 同义；查询计数时 Forward 表示
// “以该实例为尾的正向链接”，Backward 同理。
type CountDirection = Direction

// Link 是两个对象实例之间一条已登记的链接实例。
// 撤销（DeleteLink）后该实例不再计入任何索引，也不会被复用：
// 即便同样的区分属性组合被重新占用，也会生成全新的 Link（全新 ID，
// 不继承 annotations 等派生状态）。
type Link struct {
	id          string
	linkTypeID  string
	direction   Direction
	tailID      string
	headID      string
	discrim     map[string]string
	annotations map[string]string // 模拟“派生状态”：撤销后随实例一起消失
}

func (l *Link) ID() string           { return l.id }
func (l *Link) LinkTypeID() string   { return l.linkTypeID }
func (l *Link) Direction() Direction { return l.direction }
func (l *Link) TailID() string       { return l.tailID }
func (l *Link) HeadID() string       { return l.headID }

// Discriminator 返回区分属性组合的副本。
func (l *Link) Discriminator() map[string]string { return cloneAttrs(l.discrim) }

// linkKey 是“链接类型 + 有序对象对 + 方向 + 区分属性组合”的确定性身份。
// 撤销即从索引中删除该键，因此同一键可以被新链接重新占用。
type linkKey struct {
	linkTypeID string
	direction  Direction
	tailID     string
	headID     string
	discrim    string
}

// countKey 是某个方向、某个尾实例的基数桶。
// 两个方向、不同链接类型、不同尾实例分别成桶，互不影响。
type countKey struct {
	linkTypeID string
	direction  Direction
	tailID     string
}

// CreateLinkInput 是创建请求的完整输入（审计记录将原样保留）。
type CreateLinkInput struct {
	LinkTypeID    string
	Direction     Direction
	TailID        string
	HeadID        string
	Discriminator map[string]string
}

// DecisionRecord 记录一次变更请求的输入、判定依据与结果，供事后核对。
type DecisionRecord struct {
	Seq       int64
	Operation string // "create_link" | "delete_link" | "delete_object"
	Input     string
	Basis     []string
	Result    DecisionCode // CodeAccepted 或某个失败码
	LinkID    string       // 成功创建时为新链接 ID；重复时为在库链接 ID
	Detail    string
	// 结构化负载，供离线串行化回放：创建请求为输入副本；
	// 删除/逻辑删除时 TargetID 为目标 ID。
	CreateInput CreateLinkInput
	TargetID    string
}

// Store 是链接实例层的仲裁器。所有状态变更都在同一把互斥锁下完成，
// 因而并发请求天然等价于按审计序（Seq）逐一串行处理。
type Store struct {
	mu sync.Mutex

	objectTypes map[string]*ObjectType
	linkTypes   map[string]*LinkType
	objects     map[string]*Object

	// 活跃索引：仅包含当前有效的链接。撤销即物理删除，
	// 历史链接不留在任何参与判定/计数的结构中。
	links  map[linkKey]*Link
	byID   map[string]*Link
	counts map[countKey]int // 每个 (类型,方向,尾实例) 的当前计数

	seq     int64
	linkSeq int64
	audit   []DecisionRecord
}

// NewStore 创建空仲裁器。
func NewStore() *Store {
	return &Store{
		objectTypes: map[string]*ObjectType{},
		linkTypes:   map[string]*LinkType{},
		objects:     map[string]*Object{},
		links:       map[linkKey]*Link{},
		byID:        map[string]*Link{},
		counts:      map[countKey]int{},
	}
}

// RegisterObjectType 登记对象类型声明。
func (s *Store) RegisterObjectType(t *ObjectType) error {
	if t == nil || t.ID() == "" {
		return fmt.Errorf("ontology: invalid object type")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objectTypes[t.ID()]; exists {
		return fmt.Errorf("ontology: object type %q already registered", t.ID())
	}
	s.objectTypes[t.ID()] = t
	return nil
}

// RegisterLinkType 登记链接类型声明（含双向基数与区分属性列表）。
func (s *Store) RegisterLinkType(lt *LinkType) error {
	if lt == nil || lt.ID() == "" {
		return fmt.Errorf("ontology: invalid link type")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.linkTypes[lt.ID()]; exists {
		return fmt.Errorf("ontology: link type %q already registered", lt.ID())
	}
	if _, ok := s.objectTypes[lt.SourceType()]; !ok {
		return fmt.Errorf("ontology: source object type %q is not registered", lt.SourceType())
	}
	if _, ok := s.objectTypes[lt.TargetType()]; !ok {
		return fmt.Errorf("ontology: target object type %q is not registered", lt.TargetType())
	}
	s.linkTypes[lt.ID()] = lt
	return nil
}

// CreateObject 登记一个对象实例。
func (s *Store) CreateObject(ctx context.Context, id, objectType string) (*Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objectTypes[objectType]; !ok {
		return nil, fmt.Errorf("ontology: object type %q is not registered", objectType)
	}
	if _, exists := s.objects[id]; exists {
		return nil, fmt.Errorf("ontology: object %q already exists", id)
	}
	obj := NewObject(id, objectType)
	s.objects[id] = obj
	return obj, nil
}

// DeleteObject 对对象实例做逻辑删除。之后任何引用该实例的创建请求
// 一律以 ErrObjectNotFound 失败，且该判定优先于基数/重复性判定。
func (s *Store) DeleteObject(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	basis := []string{fmt.Sprintf("object %q lookup", id)}
	if !ok || !obj.IsAlive() {
		s.recordLocked("delete_object", fmt.Sprintf("id=%q", id), basis, CodeObjectNotFound, "", "object instance never registered", CreateLinkInput{}, id)
		return withBasis(ErrObjectNotFound, basis...)
	}
	obj.deleted = true
	basis = append(basis, "logical-delete flag set; existing links remain until individually revoked")
	s.recordLocked("delete_object", fmt.Sprintf("id=%q", id), basis, CodeAccepted, "", "", CreateLinkInput{}, id)
	return nil
}

// CreateLink 执行创建仲裁。判定顺序固定：
//  1. 两个被引用实例都存在且未逻辑删除（否则 ErrObjectNotFound，优先级最高）；
//  2. 链接类型存在且允许这两个对象类型按该方向建链（ErrLinkTypeNotAllowed）；
//  3. 区分属性组合与在库链接冲突 => ErrDuplicateLink（不占基数名额）；
//  4. 目标方向基数已满 => ErrCardinalityFull。
//
// 任一失败都在任何写操作之前返回，不对计数、链接集合或派生状态产生可观察影响。
func (s *Store) CreateLink(ctx context.Context, in CreateLinkInput) (*Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inputText := formatCreateInput(in)

	// 1) 实例存在性（固定最高优先级）。
	tail, ok1 := s.objects[in.TailID]
	head, ok2 := s.objects[in.HeadID]
	if !ok1 || !tail.IsAlive() || !ok2 || !head.IsAlive() {
		missing := in.TailID
		if !ok2 || !head.IsAlive() {
			missing = in.HeadID
		}
		basis := []string{
			fmt.Sprintf("tail %q exists=%v alive=%v", in.TailID, ok1, ok1 && tail.IsAlive()),
			fmt.Sprintf("head %q exists=%v alive=%v", in.HeadID, ok2, ok2 && head.IsAlive()),
			"object existence takes precedence over applicability/duplicate/cardinality",
		}
		s.recordLocked("create_link", inputText, basis, CodeObjectNotFound, "", "missing or deleted instance: "+missing, in, "")
		return nil, withBasis(ErrObjectNotFound, basis...)
	}

	// 2) 链接类型与方向适用性。
	lt, ok := s.linkTypes[in.LinkTypeID]
	if !ok {
		basis := []string{fmt.Sprintf("link type %q lookup: registered=false", in.LinkTypeID)}
		s.recordLocked("create_link", inputText, basis, CodeLinkTypeNotFound, "", "", in, "")
		return nil, withBasis(ErrLinkTypeNotFound, basis...)
	}
	if !lt.Allows(in.Direction, tail.Type(), head.Type()) {
		basis := []string{
			fmt.Sprintf("link type %q direction %s declared for %s -> %s; requested %s -> %s",
				lt.ID(), in.Direction, lt.sourceType, lt.targetType, tail.Type(), head.Type()),
		}
		s.recordLocked("create_link", inputText, basis, CodeLinkTypeNotAllowed, "", "", in, "")
		return nil, withBasis(ErrLinkTypeNotAllowed, basis...)
	}

	// 3) 重复判定（仅在同一链接类型、同一有序方向、同一对象对、
	// 同一区分属性组合的“在库”链接范围内）。撤销造成的空位不算在库。
	discrimText := canonicalDiscriminator(lt, in.Discriminator)
	key := linkKey{
		linkTypeID: lt.ID(),
		direction:  in.Direction,
		tailID:     in.TailID,
		headID:     in.HeadID,
		discrim:    discrimText,
	}
	if existing, dup := s.links[key]; dup {
		basis := []string{
			"active link exists with the same (linkType, ordered object pair, direction, discriminator tuple)",
			"duplicate declarations do not consume cardinality quota",
			"existing_link_id=" + existing.id,
		}
		s.recordLocked("create_link", inputText, basis, CodeDuplicateLink, existing.id, "", in, "")
		return existing, withBasis(ErrDuplicateLink, basis...)
	}

	// 4) 目标方向基数。计数桶与另一方向、另一链接类型完全独立。
	ck := countKey{linkTypeID: lt.ID(), direction: in.Direction, tailID: in.TailID}
	current := s.counts[ck]
	cap := lt.Cap(in.Direction)
	if limit, finite := cap.Limit(); finite && uint64(current) >= limit {
		basis := []string{
			fmt.Sprintf("active count of (%s,%s,tail=%s) = %d", lt.ID(), in.Direction, in.TailID, current),
			fmt.Sprintf("declared cap = %s", cap.String()),
			"rejection leaves all counters and the active link set unchanged",
		}
		s.recordLocked("create_link", inputText, basis, CodeCardinalityFull, "", "", in, "")
		return nil, withBasis(ErrCardinalityFull, basis...)
	}

	// 通过：登记新实例、计数加一。新链接拥有全新 ID 与空派生状态。
	s.linkSeq++
	link := &Link{
		id:          fmt.Sprintf("link-%d", s.linkSeq),
		linkTypeID:  lt.ID(),
		direction:   in.Direction,
		tailID:      in.TailID,
		headID:      in.HeadID,
		discrim:     cloneAttrs(in.Discriminator),
		annotations: map[string]string{},
	}
	s.links[key] = link
	s.byID[link.id] = link
	s.counts[ck] = current + 1
	basis := []string{
		"both instances alive",
		"direction applicable",
		"discriminator tuple free (no active duplicate)",
		fmt.Sprintf("cardinality %d < cap %s; counter incremented to %d", current, cap.String(), current+1),
	}
	s.recordLocked("create_link", inputText, basis, CodeAccepted, link.id, "", in, "")
	return link, nil
}

// DeleteLink 撤销一条链接：从所有活跃索引中物理移除并立即释放
// 对应方向的基数名额。与“同键新建”并发时，二者在互斥锁下串行，
// 最终状态必然等价于“删在前”或“建在前”两种串行序之一。
func (s *Store) DeleteLink(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	link, ok := s.byID[id]
	if !ok {
		basis := []string{fmt.Sprintf("link id %q is absent from the active index (revoked links never reappear)", id)}
		s.recordLocked("delete_link", fmt.Sprintf("id=%q", id), basis, CodeLinkNotFound, "", "", CreateLinkInput{}, id)
		return withBasis(ErrLinkNotFound, basis...)
	}

	key := linkKey{
		linkTypeID: link.linkTypeID,
		direction:  link.direction,
		tailID:     link.tailID,
		headID:     link.headID,
		discrim:    s.discriminatorTextLocked(link),
	}
	ck := countKey{linkTypeID: link.linkTypeID, direction: link.direction, tailID: link.tailID}
	delete(s.links, key)
	delete(s.byID, id)
	s.counts[ck]--
	if s.counts[ck] == 0 {
		delete(s.counts, ck) // 保持桶数量只与当前仍有出链的尾实例相关
	}
	basis := []string{
		"removed from active set and id index",
		fmt.Sprintf("released one quota in (%s,%s,tail=%s)", link.linkTypeID, link.direction, link.tailID),
		"derived state dies with the instance; reoccupying the tuple creates a fresh link",
	}
	s.recordLocked("delete_link", fmt.Sprintf("id=%q", id), basis, CodeAccepted, "", "", CreateLinkInput{}, id)
	return nil
}

// CountLinks 返回某实例在给定方向上“当前有效”的链接数量。
// 开销为一次哈希查表，与该实例历史上创建/撤销过多少链接无关。
func (s *Store) CountLinks(ctx context.Context, linkTypeID, instanceID string, d CountDirection) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.linkTypes[linkTypeID]; !ok {
		return 0, withBasis(ErrLinkTypeNotFound, "link type lookup: registered=false")
	}
	return s.counts[countKey{linkTypeID: linkTypeID, direction: d, tailID: instanceID}], nil
}

// ActiveLinks 返回某链接类型当前全部有效链接的快照（按 ID 排序）。
func (s *Store) ActiveLinks(linkTypeID string) []*Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Link
	for _, l := range s.links {
		if l.linkTypeID == linkTypeID {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// AnnotateLink 在链接实例上写入一项派生状态。链接撤销后其派生状态
// 随之消失；占用同一区分属性组合的新链接不会读到旧值。
func (s *Store) AnnotateLink(ctx context.Context, linkID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.byID[linkID]
	if !ok {
		return withBasis(ErrLinkNotFound, "link absent from active index")
	}
	l.annotations[key] = value
	return nil
}

// Annotation 读取链接实例上的派生状态。
func (s *Store) Annotation(linkID, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.byID[linkID]
	if !ok {
		return "", false
	}
	v, ok := l.annotations[key]
	return v, ok
}

// AuditLog 返回截至调用时刻的审计记录副本（按全序 Seq 排列）。
func (s *Store) AuditLog() []DecisionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DecisionRecord, len(s.audit))
	copy(out, s.audit)
	return out
}

func (s *Store) recordLocked(op, input string, basis []string, result DecisionCode, linkID, detail string, in CreateLinkInput, targetID string) {
	s.seq++
	s.audit = append(s.audit, DecisionRecord{
		Seq:       s.seq,
		Operation: op,
		Input:     input,
		Basis:     append([]string(nil), basis...),
		Result:    result,
		LinkID:    linkID,
		Detail:    detail,
		CreateInput: CreateLinkInput{
			LinkTypeID:    in.LinkTypeID,
			Direction:     in.Direction,
			TailID:        in.TailID,
			HeadID:        in.HeadID,
			Discriminator: cloneAttrs(in.Discriminator),
		},
		TargetID: targetID,
	})
}

func (s *Store) discriminatorTextLocked(l *Link) string {
	lt := s.linkTypes[l.linkTypeID]
	return canonicalDiscriminator(lt, l.discrim)
}

func cloneAttrs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func formatCreateInput(in CreateLinkInput) string {
	return fmt.Sprintf("linkType=%q direction=%s tail=%q head=%q discrim={%s}",
		in.LinkTypeID, in.Direction, in.TailID, in.HeadID, formatAttrs(in.Discriminator))
}

// canonicalDiscriminator 按链接类型声明的区分属性顺序、辅以属性名排序，
// 把属性组合归一成确定性字符串，避免 map 遍历随机序影响重复判定。
func canonicalDiscriminator(lt *LinkType, attrs map[string]string) string {
	names := make([]string, 0, len(attrs))
	declared := map[string]struct{}{}
	if lt != nil {
		for _, n := range lt.DiscriminatorAttrs() {
			names = append(names, n)
			declared[n] = struct{}{}
		}
	}
	var extra []string
	for k := range attrs {
		if _, ok := declared[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	names = append(names, extra...)

	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(encodeKV(name))
		b.WriteByte('=')
		b.WriteString(encodeKV(attrs[name]))
	}
	return b.String()
}

func formatAttrs(attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, encodeKV(k)+"="+encodeKV(attrs[k]))
	}
	return strings.Join(parts, ",")
}

// encodeKV 用百分号转义分隔符，保证键值中的 '&'、'='、'%' 不会破坏规范性。
func encodeKV(v string) string {
	if !strings.ContainsAny(v, "&=%") {
		return v
	}
	var b strings.Builder
	for _, r := range v {
		switch r {
		case '&':
			b.WriteString("%26")
		case '=':
			b.WriteString("%3D")
		case '%':
			b.WriteString("%25")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
