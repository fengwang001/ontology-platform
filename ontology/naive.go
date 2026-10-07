package ontology

// naiveLink 是朴素模型里的链接记录。
type naiveLink struct {
	id    int
	ltID  string
	dir   Direction
	tail  string
	head  string
	discr map[string]string
}

// NaiveStore 是一份刻意“直白”的独立参考实现：
// 不加锁、不做增量索引，每次判定都线性扫描全部当前链接。
// 它与 Store 共享同一套语义规则与错误分类，但不共享任何数据结构，
// 供并发随机测试做最终状态对拍。仅应在单 goroutine 中使用。
type NaiveStore struct {
	types map[string]struct{}
	links map[string]*LinkType

	objects map[string]string // id -> type
	deleted map[string]bool

	active []naiveLink
	nextID int
}

// NewNaiveStore 创建朴素参考模型。
func NewNaiveStore() *NaiveStore {
	return &NaiveStore{
		types:   map[string]struct{}{},
		links:   map[string]*LinkType{},
		objects: map[string]string{},
		deleted: map[string]bool{},
	}
}

// RegisterObjectType 登记对象类型。
func (n *NaiveStore) RegisterObjectType(t *ObjectType) { n.types[t.ID()] = struct{}{} }

// RegisterLinkType 登记链接类型。
func (n *NaiveStore) RegisterLinkType(lt *LinkType) { n.links[lt.ID()] = lt }

// CreateObject 登记实例。
func (n *NaiveStore) CreateObject(id, objectType string) { n.objects[id] = objectType }

// DeleteObject 逻辑删除实例。
func (n *NaiveStore) DeleteObject(id string) bool {
	if _, ok := n.objects[id]; !ok || n.deleted[id] {
		return false
	}
	n.deleted[id] = true
	return true
}

func (n *NaiveStore) alive(id string) bool {
	_, registered := n.objects[id]
	return registered && !n.deleted[id]
}

// CreateLink 按与 Store 完全相同的优先级规则串行处理一次创建。
// 返回新链接的数字 ID（重复时返回在库链接 ID）与结果码。
func (n *NaiveStore) CreateLink(in CreateLinkInput) (int, DecisionCode) {
	if !n.alive(in.TailID) || !n.alive(in.HeadID) {
		return 0, CodeObjectNotFound
	}
	lt, ok := n.links[in.LinkTypeID]
	if !ok {
		return 0, CodeLinkTypeNotFound
	}
	if !lt.Allows(in.Direction, n.objects[in.TailID], n.objects[in.HeadID]) {
		return 0, CodeLinkTypeNotAllowed
	}
	discrText := canonicalDiscriminator(lt, in.Discriminator)
	for i := range n.active {
		l := &n.active[i]
		if l.ltID == in.LinkTypeID && l.dir == in.Direction && l.tail == in.TailID &&
			l.head == in.HeadID && canonicalDiscriminator(lt, l.discr) == discrText {
			return l.id, CodeDuplicateLink
		}
	}
	var count int
	for i := range n.active {
		l := &n.active[i]
		if l.ltID == in.LinkTypeID && l.dir == in.Direction && l.tail == in.TailID {
			count++
		}
	}
	cap := lt.Cap(in.Direction)
	if limit, finite := cap.Limit(); finite && uint64(count) >= limit {
		return 0, CodeCardinalityFull
	}
	n.nextID++
	n.active = append(n.active, naiveLink{
		id: n.nextID, ltID: in.LinkTypeID, dir: in.Direction,
		tail: in.TailID, head: in.HeadID, discr: cloneAttrs(in.Discriminator),
	})
	return n.nextID, CodeAccepted
}

// DeleteLink 按数字 ID 删除当前链接；删除不存在的 ID 返回 false。
func (n *NaiveStore) DeleteLink(id int) bool {
	for i := range n.active {
		if n.active[i].id == id {
			n.active = append(n.active[:i], n.active[i+1:]...)
			return true
		}
	}
	return false
}

// Count 返回某 (链接类型,方向,尾实例) 的当前计数，线性扫描。
func (n *NaiveStore) Count(ltID string, d Direction, tail string) int {
	count := 0
	for i := range n.active {
		l := &n.active[i]
		if l.ltID == ltID && l.dir == d && l.tail == tail {
			count++
		}
	}
	return count
}

// ActiveSignature 返回当前链接集合的规范化签名（多重集），用于对拍。
func (n *NaiveStore) ActiveSignature(ltID string) []string {
	lt := n.links[ltID]
	var out []string
	for i := range n.active {
		l := &n.active[i]
		if l.ltID == ltID {
			out = append(out, lt.signatureOf(l))
		}
	}
	sortStrings(out)
	return out
}

// signatureOf 不包含链接自身 ID：撤销后重占得到新 ID，
// 但最终“在库内容”应当一致。
func (lt *LinkType) signatureOf(l *naiveLink) string {
	return l.ltID + "|" + l.dir.String() + "|" + l.tail + "|" + l.head +
		"|" + canonicalDiscriminator(lt, l.discr)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
