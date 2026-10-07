package idx

// epoch 是一个索引版本：以某个属性字段为依据的倒排索引。
// 查询开销只取决于取值基数与命中对象数，与累计处理的事件总量无关：
// 结构体规模上界为 O(对象数 + 不同取值数)，不保存事件历史。
type epoch struct {
	id       int
	basis    string                         // 索引依据属性字段
	inverted map[string]map[string]struct{} // 取值 -> 对象集合
	byObject map[string]string              // 对象 -> 当前索引取值（O(1) 摘除旧 posting）
}

func newEpoch(id int, basis string) *epoch {
	return &epoch{
		id:       id,
		basis:    basis,
		inverted: make(map[string]map[string]struct{}),
		byObject: make(map[string]string),
	}
}

// set 将对象的索引取值更新为 (value, null)。
func (e *epoch) set(obj, value string, null bool) {
	if old, ok := e.byObject[obj]; ok {
		if old == value && !null {
			return
		}
		if posting := e.inverted[old]; posting != nil {
			delete(posting, obj)
			if len(posting) == 0 {
				delete(e.inverted, old)
			}
		}
	}
	if null {
		delete(e.byObject, obj)
		return
	}
	e.byObject[obj] = value
	posting := e.inverted[value]
	if posting == nil {
		posting = make(map[string]struct{})
		e.inverted[value] = posting
	}
	posting[obj] = struct{}{}
}

// lookup 返回取值为 value 的对象集合（内部表示，调用方不得修改）。
func (e *epoch) lookup(value string) map[string]struct{} {
	return e.inverted[value]
}

// postingSize 返回索引内部 posting 的总规模，用于结构不变量校验。
func (e *epoch) postingSize() int {
	n := 0
	for _, posting := range e.inverted {
		n += len(posting)
	}
	return n
}
