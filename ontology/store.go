package ontology

// linkStore 是链接的权威集合，按 (链接类型, 有序实例对) 去重。
//
// (A,B) 与 (B,A) 是不同的有序对，可分别存在；同类型同有序对的第二条链接
// 视为参数非法（KindInvalidArgument）。不同链接类型即使连接同一对实例也
// 各自独立。
//
// store 自身不加锁，与 ledger 一样只在 service 的单一临界区内被访问。
type linkStore struct {
	links map[linkKey]struct{}
}

type linkKey struct {
	linkType string
	source   string
	target   string
}

func newLinkStore() *linkStore { return &linkStore{links: map[linkKey]struct{}{}} }

func (st *linkStore) exists(linkType string, p Pair) bool {
	_, ok := st.links[linkKey{linkType, p.Source, p.Target}]
	return ok
}

// add 要求调用方已通过 exists 检查；返回 false 表示键已存在（防御式）。
func (st *linkStore) add(linkType string, p Pair) bool {
	k := linkKey{linkType, p.Source, p.Target}
	if _, ok := st.links[k]; ok {
		return false
	}
	st.links[k] = struct{}{}
	return true
}

// remove 删除一条链接；返回 false 表示链接不存在（归一化为 KindNotFound）。
func (st *linkStore) remove(linkType string, p Pair) bool {
	k := linkKey{linkType, p.Source, p.Target}
	if _, ok := st.links[k]; !ok {
		return false
	}
	delete(st.links, k)
	return true
}

// snapshot 返回当前链接集合的稳定副本，供测试与朴素模型逐条对账。
func (st *linkStore) snapshot() []StoredLink {
	out := make([]StoredLink, 0, len(st.links))
	for k := range st.links {
		out = append(out, StoredLink{LinkType: k.linkType, Pair: Pair{k.source, k.target}})
	}
	return out
}

func (st *linkStore) len() int { return len(st.links) }
