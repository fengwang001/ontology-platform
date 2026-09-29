package antientropy

import (
	"fmt"
	"sort"
)

// NewRegistry 创建一个空的副本注册表。
func NewRegistry() *Registry {
	return &Registry{replicas: map[string]*Replica{}}
}

// Register 创建并注册一个具名副本；空名或重名返回 ErrUnregisteredReplica。
func (r *Registry) Register(name string, cfg Config) (*Replica, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty name", ErrUnregisteredReplica)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.replicas[name]; ok {
		return nil, fmt.Errorf("%w: duplicate name %q", ErrUnregisteredReplica, name)
	}
	if cfg.MaxBatch < 0 {
		cfg.MaxBatch = 0
	}
	rep := &Replica{
		name:     name,
		reg:      r,
		vector:   Version{},
		idx:      map[string][]Change{},
		maxBatch: cfg.MaxBatch,
	}
	r.replicas[name] = rep
	return rep, nil
}

// Get 返回已注册副本。
func (r *Registry) Get(name string) (*Replica, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rep, ok := r.replicas[name]
	return rep, ok
}

// Names 返回全部已注册副本名（字典序）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.replicas))
	for name := range r.replicas {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

// Name 返回副本名。
func (r *Replica) Name() string { return r.name }

// Write 在本地追加一条写入，推进本来源序号与版本向量。
func (r *Replica) Write(key, value string) Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock++
	seq := r.vector[r.name] + 1
	c := Change{Source: r.name, Seq: seq, Key: key, Value: value, TS: r.clock}
	r.vector[r.name] = seq
	r.idx[r.name] = append(r.idx[r.name], c)
	r.insertSorted(c)
	return c
}

// Vector 返回版本向量的深拷贝。
func (r *Replica) Vector() Version {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(Version, len(r.vector))
	for k, v := range r.vector {
		out[k] = v
	}
	return out
}

// Log 返回本地日志的拷贝（按 Source、Seq 有序）。
func (r *Replica) Log() []Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Change(nil), r.log...)
}

// View 返回读视图：每个键取 (TS, Source, Seq) 字典序最大者的值。
func (r *Replica) View() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.viewLocked()
}

// insertSorted 按 (Source, Seq) 将变更插入日志的正确位置。
func (r *Replica) insertSorted(c Change) {
	pos := sort.Search(len(r.log), func(i int) bool { return !lessChange(r.log[i], c) })
	r.log = append(r.log, Change{})
	copy(r.log[pos+1:], r.log[pos:])
	r.log[pos] = c
}

// lessChange 定义 (Source, Seq) 的全序。
func lessChange(a, b Change) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Seq < b.Seq
}

// wins 判定 a 是否在读视图裁决中胜出：(TS, Source, Seq) 字典序更大。
func (a Change) wins(b Change) bool {
	if a.TS != b.TS {
		return a.TS > b.TS
	}
	if a.Source != b.Source {
		return a.Source > b.Source
	}
	return a.Seq > b.Seq
}

// viewLocked 要求持有 r.mu。
func (r *Replica) viewLocked() map[string]string {
	winner := map[string]Change{}
	for _, c := range r.log {
		if cur, ok := winner[c.Key]; !ok || c.wins(cur) {
			winner[c.Key] = c
		}
	}
	out := make(map[string]string, len(winner))
	for k, c := range winner {
		out[k] = c.Value
	}
	return out
}
