package room

// member 为在室玩家。
type member struct {
	id      string
	joinSeq int64 // 本次在室的加入次序（单调递增）
	ready   bool
}

// roster 管理在室玩家、就绪计数与房主。
//
// 全体就绪判断通过 readyCnt 计数器 O(1) 完成，
// 不随在室人数线性增长；work 计数器记录循环工作量，
// 供测试验证复杂度上界。
type roster struct {
	members  map[string]*member
	readyCnt int64
	nextSeq  int64
	host     string
	work     *int64
}

func newRoster(work *int64) *roster {
	return &roster{members: make(map[string]*member), work: work}
}

func (r *roster) size() int64 { return int64(len(r.members)) }

func (r *roster) has(id string) bool {
	_, ok := r.members[id]
	return ok
}

func (r *roster) get(id string) *member { return r.members[id] }

// add 加入新成员（未就绪）。首个加入者成为房主。
func (r *roster) add(id string) {
	r.members[id] = &member{id: id, joinSeq: r.nextSeq}
	r.nextSeq++
	if r.host == "" {
		r.host = id
	}
}

// remove 移除成员并维护就绪计数；若移除的是房主则迁移。
func (r *roster) remove(id string) {
	m, ok := r.members[id]
	if !ok {
		return
	}
	if m.ready {
		r.readyCnt--
	}
	delete(r.members, id)
	if r.host == id {
		r.migrateHost()
	}
}

// setReady 设置就绪标记并维护计数；无变化时不产生任何效果。
func (r *roster) setReady(id string, on bool) {
	m := r.members[id]
	if m.ready == on {
		return
	}
	m.ready = on
	if on {
		r.readyCnt++
	} else {
		r.readyCnt--
	}
}

// allReady O(1) 判断全体在室玩家是否就绪。
func (r *roster) allReady() bool {
	return len(r.members) > 0 && r.readyCnt == int64(len(r.members))
}

// migrateHost 把房主迁移给在室玩家中加入次序最早者。
// 扫描上界为 U<=20，不随历史事件数增长。
func (r *roster) migrateHost() {
	r.host = ""
	var best int64
	for id, m := range r.members {
		*r.work++
		if r.host == "" || m.joinSeq < best {
			r.host = id
			best = m.joinSeq
		}
	}
}

// joinOrdered 返回按加入次序排序的在室成员（规模上界 U<=20）。
func (r *roster) joinOrdered() []*member {
	out := make([]*member, 0, len(r.members))
	for _, m := range r.members {
		*r.work++
		out = append(out, m)
	}
	// 插入排序：规模有界（<=20），无需引入 sort。
	for i := 1; i < len(out); i++ {
		*r.work++
		for j := i; j > 0 && out[j].joinSeq < out[j-1].joinSeq; j-- {
			*r.work++
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
