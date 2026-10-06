package mirror

// dirtyLog 是每个非在线成员各持有一份的脏区记录。
// 对故障成员，它登记其缺席的块，不同块数严格超过上限时记录被丢弃，
// 并永久标记 fullResync 直到下一次重同步完成。
// 对重同步中成员，它同时充当待同步块游标：写入直接落到该成员时
// 清除对应块标记，重同步推进时按升序弹出。
type dirtyLog struct {
	set        blockSet
	limit      int
	fullResync bool
}

func newDirtyLog(limit int, ops *uint64) *dirtyLog {
	d := &dirtyLog{limit: limit}
	d.set.ops = ops
	return d
}

// add 登记一个缺失块。记录已丢弃（fullResync）时不再登记。
func (d *dirtyLog) add(block int) {
	if d.fullResync {
		return
	}
	d.set.insert(block)
	if d.set.len() > d.limit {
		d.set = blockSet{ops: d.set.ops}
		d.fullResync = true
	}
}

// remove 清除一个块的待同步标记；块不存在时为空操作。
func (d *dirtyLog) remove(block int) {
	d.set.remove(block)
}

// fillAll 把记录重置为全部块，用于全量重同步。
func (d *dirtyLog) fillAll(numBlocks int) {
	d.set = blockSet{ops: d.set.ops}
	for b := 0; b < numBlocks; b++ {
		d.set.insert(b)
	}
	d.fullResync = true
}
