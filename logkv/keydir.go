package logkv

// keydirEntry 是键目录中一个键的最新记录位置。
type keydirEntry struct {
	Segment   uint32
	Offset    uint64
	Length    uint32
	Seq       uint64
	Tombstone bool
}

// keydir 是内存键目录：每个键只保留写序号最大的那条记录的位置。
type keydir struct {
	m map[string]keydirEntry
}

func newKeydir() *keydir {
	return &keydir{m: make(map[string]keydirEntry)}
}

// apply 按“写序号最大者胜”的规则登记一条记录位置。
func (k *keydir) apply(key []byte, e keydirEntry) {
	cur, ok := k.m[string(key)]
	if ok && cur.Seq >= e.Seq {
		return
	}
	k.m[string(key)] = e
}

func (k *keydir) lookup(key []byte) (keydirEntry, bool) {
	e, ok := k.m[string(key)]
	return e, ok
}

// removeSegment 删除指向某个段的全部条目，用于自愈重建前清场。
func (k *keydir) removeSegment(segID uint32) {
	for key, e := range k.m {
		if e.Segment == segID {
			delete(k.m, key)
		}
	}
}

func (k *keydir) delete(key string) {
	delete(k.m, key)
}
