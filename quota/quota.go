// Package quota 按本地日累计账号已用时长。
package quota

// Quota 是每日已用秒数账本。
type Quota struct {
	used map[int64]int64
}

// New 创建空账本。
func New() *Quota {
	return &Quota{used: map[int64]int64{}}
}

// Add 向 day 累加 d 秒，上限为 limit，返回实际入账量。
func (q *Quota) Add(day, d, limit int64) int64 {
	if d <= 0 {
		return 0
	}
	have := q.used[day]
	if have >= limit {
		return 0
	}
	room := limit - have
	if d > room {
		d = room
	}
	q.used[day] = have + d
	return d
}

// Used 返回某日已用。
func (q *Quota) Used(day int64) int64 { return q.used[day] }

// Snapshot 返回已用账本的拷贝。
func (q *Quota) Snapshot() map[int64]int64 {
	m := make(map[int64]int64, len(q.used))
	for d, v := range q.used {
		m[d] = v
	}
	return m
}

// Restore 用快照覆盖账本。
func (q *Quota) Restore(m map[int64]int64) {
	q.used = make(map[int64]int64, len(m))
	for d, v := range m {
		q.used[d] = v
	}
}
