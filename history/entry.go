package history

// Entry 是一条历史条目。Seq 全局单调递增，替换导航不改变它。
type Entry struct {
	URL   string
	DocID string
	State any
	Seq   uint64
}

// entryIndex 维护 文档标识 -> 引用它的条目下标集合。
// 条目只会从尾部追加或截断，下标稳定，因此可以用 int 集合做反向索引，
// 使"同文档全部条目更新"与"截断后引用计数"的开销与历史总长无关。
type entryIndex map[string]map[int]struct{}

func newEntryIndex() entryIndex { return make(entryIndex) }

func (ix entryIndex) add(docID string, i int) {
	set, ok := ix[docID]
	if !ok {
		set = make(map[int]struct{})
		ix[docID] = set
	}
	set[i] = struct{}{}
}

// remove 删除一个引用，返回该文档是否不再被任何条目引用。
func (ix entryIndex) remove(docID string, i int) bool {
	set, ok := ix[docID]
	if !ok {
		return true
	}
	delete(set, i)
	if len(set) == 0 {
		delete(ix, docID)
		return true
	}
	return false
}

func (ix entryIndex) refcount(docID string) int { return len(ix[docID]) }
