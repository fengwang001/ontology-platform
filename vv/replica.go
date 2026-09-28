package vv

import (
	"sort"
	"strings"
)

// NewReplica 在注册表内创建一个副本。未注册名称整体拒绝。
func NewReplica(r *Registry, name string) (*Replica, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.members[name] {
		return nil, newError(KindUnregisteredReplica, "cannot create replica %q", name)
	}
	if _, exists := r.replicas[name]; exists {
		return nil, newError(KindUnregisteredReplica, "replica %q already exists", name)
	}
	rep := &Replica{
		registry: r,
		name:     name,
		log:      []Change{},
		vector:   VersionVector{},
	}
	r.replicas[name] = rep
	return rep, nil
}

// Write 执行一次本地写入：来源序号加一，按 (来源, 序号) 有序追加日志并推进版本向量。
func (rep *Replica) Write(timestamp, key, value string) (Change, error) {
	rep.mu.Lock()
	defer rep.mu.Unlock()

	if rep.registry.maxLog > 0 && len(rep.log) >= rep.registry.maxLog {
		return Change{}, newError(KindLogLimitExceeded,
			"replica %q log already holds %d entries (limit %d)",
			rep.name, len(rep.log), rep.registry.maxLog)
	}

	change := Change{
		Origin:    rep.name,
		Seq:       rep.vector[rep.name] + 1,
		Key:       key,
		Value:     value,
		Timestamp: timestamp,
	}
	insertSorted(&rep.log, change)
	rep.vector[rep.name] = change.Seq
	return change, nil
}

// Vector 返回当前版本向量的快照副本。
func (rep *Replica) Vector() VersionVector {
	rep.mu.Lock()
	defer rep.mu.Unlock()
	out := make(VersionVector, len(rep.vector))
	for k, v := range rep.vector {
		out[k] = v
	}
	return out
}

// Log 返回当前变更日志按 (来源, 序号) 排序的快照。
func (rep *Replica) Log() []Change {
	rep.mu.Lock()
	defer rep.mu.Unlock()
	out := make([]Change, len(rep.log))
	copy(out, rep.log)
	return out
}

// View 返回当前键值读视图：对每个键取 (时间戳, 来源, 序号)
// 字典序最大的那条变更的值。
func (rep *Replica) View() map[string]string {
	rep.mu.Lock()
	defer rep.mu.Unlock()
	return viewFromLog(rep.log)
}

func viewFromLog(log []Change) map[string]string {
	type winner struct {
		rank string
		val  string
	}
	winners := map[string]winner{}
	for _, c := range log {
		rank := changeRank(c)
		cur, ok := winners[c.Key]
		if !ok || rank > cur.rank {
			winners[c.Key] = winner{rank: rank, val: c.Value}
		}
	}
	view := make(map[string]string, len(winners))
	for k, w := range winners {
		view[k] = w.val
	}
	return view
}

// changeRank 构造 (时间戳, 来源, 序号) 的字典序可比较表示。
// 序号采用固定宽度零填充，避免字符串序与数值序不一致。
func changeRank(c Change) string {
	return c.Timestamp + "\x00" + c.Origin + "\x00" + padSeq(c.Seq)
}

func padSeq(seq int64) string {
	const width = 19
	s := itoa(seq)
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func insertSorted(log *[]Change, c Change) {
	i := sort.Search(len(*log), func(i int) bool {
		return changeLess(c, (*log)[i])
	})
	*log = append(*log, Change{})
	copy((*log)[i+1:], (*log)[i:])
	(*log)[i] = c
}

func changeLess(a, b Change) bool {
	if a.Origin != b.Origin {
		return a.Origin < b.Origin
	}
	return a.Seq < b.Seq
}
