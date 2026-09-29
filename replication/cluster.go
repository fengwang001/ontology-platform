package replication

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Cluster 持有多个副本，并提供领导者侧与恢复侧 API。
type Cluster struct {
	mu       sync.RWMutex
	replicas map[string]*Replica
	logger   *slog.Logger
}

// NewCluster 创建空集群。
func NewCluster(logger *slog.Logger) *Cluster {
	if logger == nil {
		logger = slog.Default()
	}
	return &Cluster{replicas: make(map[string]*Replica), logger: logger}
}

// lookup 返回已注册副本；调用方自行处理集群映射锁。
func (c *Cluster) lookup(name string) (*Replica, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty replica name", ErrInvalidArgument)
	}
	r, ok := c.replicas[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownReplica, name)
	}
	return r, nil
}

// lockReplicas 按名称排序后依次加锁，避免多副本操作死锁；返回配对的解锁函数。
func lockReplicas(replicas ...*Replica) func() {
	sort.Slice(replicas, func(i, j int) bool { return replicas[i].name < replicas[j].name })
	locked := replicas[:0]
	for _, r := range replicas {
		r.lock()
		locked = append(locked, r)
	}
	return func() {
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].unlock()
		}
	}
}

// AddReplica 注册一个新副本。
func (c *Cluster) AddReplica(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty replica name", ErrInvalidArgument)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.replicas[name]; exists {
		return fmt.Errorf("%w: replica %q already exists", ErrInvalidArgument, name)
	}
	c.replicas[name] = newReplica(name)
	c.logger.Info("add replica", "replica", name)
	return nil
}

func (c *Cluster) getOne(name string) (*Replica, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lookup(name)
}

func (c *Cluster) getMany(names []string) ([]*Replica, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := make(map[string]bool, len(names))
	out := make([]*Replica, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		r, err := c.lookup(name)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// BecomeLeader 令副本以新世代当选，并在其日志末尾登记世代起始位点。
func (c *Cluster) BecomeLeader(name string, gen int64) error {
	if gen <= 0 {
		return fmt.Errorf("%w: generation must be positive, got %d", ErrInvalidArgument, gen)
	}
	r, err := c.getOne(name)
	if err != nil {
		return err
	}
	r.lock()
	defer r.unlock()
	if len(r.gen) > 0 && gen <= r.gen[len(r.gen)-1].Gen {
		return fmt.Errorf("%w: generation %d not greater than latest %d", ErrInvalidArgument, gen, r.gen[len(r.gen)-1].Gen)
	}
	if !r.appendGenMark(gen, len(r.log)) {
		return fmt.Errorf("%w: cannot register generation %d at %d", ErrInvalidArgument, gen, len(r.log))
	}
	c.logger.Info("become leader", "replica", name, "gen", gen, "gen_start", len(r.log),
		"reason", "elected: append generation-start cache entry at current log end")
	return nil
}

// Append 领导者在本世代写入一条消息并复制到 followers。
func (c *Cluster) Append(leader string, gen int64, data string, followers []string) error {
	if gen <= 0 {
		return fmt.Errorf("%w: generation must be positive, got %d", ErrInvalidArgument, gen)
	}
	all, err := c.getMany(append(append([]string{}, followers...), leader))
	if err != nil {
		return err
	}
	unlock := lockReplicas(all...)
	defer unlock()

	lead := replicaByName(all, leader)
	start, ok := lead.genStart(gen)
	if !ok {
		return fmt.Errorf("%w: leader %q has no generation %d", ErrUnknownGeneration, leader, gen)
	}
	if lead.gen[len(lead.gen)-1].Gen != gen {
		return fmt.Errorf("%w: generation %d is not current (starts at %d)", ErrInvalidArgument, gen, start)
	}

	newEntry := Entry{Gen: gen, Data: data}
	targetLog := append(lead.cloneLog(), newEntry)
	tails := make(map[*Replica][]Entry)
	for _, r := range all {
		if r == lead {
			continue
		}
		if err := validateTail(r, targetLog); err != nil {
			c.logger.Info("append rejected", "leader", leader, "gen", gen, "follower", r.name,
				"reason", err.Error())
			return err
		}
		tails[r] = targetLog[r.len():]
	}

	if !lead.appendEntries([]Entry{newEntry}) {
		return fmt.Errorf("%w: leader append failed for gen %d", ErrInvalidArgument, gen)
	}
	for _, r := range all {
		if r == lead {
			continue
		}
		r.appendEntries(tails[r])
	}
	c.logger.Info("append", "leader", leader, "gen", gen, "data", data,
		"followers", followers, "position", len(targetLog)-1,
		"reason", "entry written on leader and copied to every follower")
	return nil
}

// GenerationEnd 领导者按请求世代返回该世代日志的可用结束位点。
func (c *Cluster) GenerationEnd(leader string, gen int64) (int, error) {
	if gen <= 0 {
		return 0, fmt.Errorf("%w: generation must be positive, got %d", ErrInvalidArgument, gen)
	}
	r, err := c.getOne(leader)
	if err != nil {
		return 0, err
	}
	r.lock()
	defer r.unlock()
	end, err := generationEndLocked(r, gen)
	if err != nil {
		return 0, err
	}
	c.logger.Info("generation end query", "leader", leader, "gen", gen, "end", end,
		"reason", "exclusive end of the positions belonging to the requested generation")
	return end, nil
}

// QueryLog 返回副本 [start,end) 范围内的日志条目（只读、可并发）。
func (c *Cluster) QueryLog(name string, start, end int) ([]Entry, error) {
	if start < 0 || end < 0 || start > end {
		return nil, fmt.Errorf("%w: bad range [%d,%d)", ErrInvalidArgument, start, end)
	}
	r, err := c.getOne(name)
	if err != nil {
		return nil, err
	}
	r.lock()
	defer r.unlock()
	if end > r.len() {
		return nil, fmt.Errorf("%w: range end %d beyond log length %d", ErrInvalidArgument, end, r.len())
	}
	return r.cloneLog()[start:end], nil
}

// LogLen 返回副本日志长度。
func (c *Cluster) LogLen(name string) (int, error) {
	r, err := c.getOne(name)
	if err != nil {
		return 0, err
	}
	r.lock()
	defer r.unlock()
	return r.len(), nil
}

// GenCache 返回副本世代缓存的快照。
func (c *Cluster) GenCache(name string) ([]GenMark, error) {
	r, err := c.getOne(name)
	if err != nil {
		return nil, err
	}
	r.lock()
	defer r.unlock()
	return r.cloneGen(), nil
}

// Replicate 把 from 在 at 之后的条目复制到 follower；重叠部分必须一致。
func (c *Cluster) Replicate(from, follower string, at int) error {
	if at < 0 {
		return fmt.Errorf("%w: position must be non-negative, got %d", ErrInvalidArgument, at)
	}
	if from == follower {
		return fmt.Errorf("%w: source and follower must differ: %q", ErrInvalidArgument, from)
	}
	rs, err := c.getMany([]string{from, follower})
	if err != nil {
		return err
	}
	unlock := lockReplicas(rs...)
	defer unlock()
	src := replicaByName(rs, from)
	fol := replicaByName(rs, follower)

	if at > fol.len() || at > src.len() {
		return fmt.Errorf("%w: position %d beyond a log length (source %d, follower %d)",
			ErrInvalidArgument, at, src.len(), fol.len())
	}
	limit := fol.len()
	if src.len() < limit {
		return fmt.Errorf("%w: follower longer than source: %d>%d",
			ErrLogDiverged, limit, src.len())
	}
	for i := 0; i < limit; i++ {
		if src.log[i] != fol.log[i] {
			return fmt.Errorf("%w: mismatch at position %d: source=%v follower=%v",
				ErrLogDiverged, i, src.log[i], fol.log[i])
		}
	}
	if err := validateTail(fol, src.log); err != nil {
		return err
	}
	tail := src.cloneLog()[fol.len():]
	fol.appendEntries(tail)
	c.logger.Info("replicate", "source", from, "follower", follower, "after", at,
		"copied", src.len()-fol.len(), "new_len", src.len(),
		"reason", "overlap matched; append the source tail to the follower")
	return nil
}

// validateTail 检查 r 的日志是否为 targetLog 的一致前缀且世代世代单调可追加。
func validateTail(r *Replica, targetLog []Entry) error {
	if r.len() > len(targetLog) {
		return fmt.Errorf("%w: follower %q longer than target: %d>%d",
			ErrLogDiverged, r.name, r.len(), len(targetLog))
	}
	for i := 0; i < r.len(); i++ {
		if r.log[i] != targetLog[i] {
			return fmt.Errorf("%w: follower %q mismatch at position %d: leader=%v follower=%v",
				ErrLogDiverged, r.name, i, targetLog[i], r.log[i])
		}
	}
	maxGen := int64(0)
	if len(r.gen) > 0 {
		maxGen = r.gen[len(r.gen)-1].Gen
	}
	for _, entry := range targetLog[r.len():] {
		if entry.Gen < maxGen {
			return fmt.Errorf("%w: follower %q cannot accept older generation %d after %d",
				ErrLogDiverged, r.name, entry.Gen, maxGen)
		}
		maxGen = entry.Gen
	}
	return nil
}

func replicaByName(rs []*Replica, name string) *Replica {
	for _, r := range rs {
		if r.name == name {
			return r
		}
	}
	return nil
}

// generationEndLocked 计算世代 gen 的独占结束位点（调用方持锁）。
func generationEndLocked(r *Replica, gen int64) (int, error) {
	start, ok := r.genStart(gen)
	if !ok {
		return 0, fmt.Errorf("%w: replica %q has no generation %d", ErrUnknownGeneration, r.name, gen)
	}
	return generationEndAtLocked(r, gen, start)
}

// generationEndAtLocked 计算世代 gen 从给定起始位点开始的独占结束位点（调用方持锁）。
// 该副本里世代的起始位点与请求不一致时，视为不同分支，按未知世代处理。
func generationEndAtLocked(r *Replica, gen int64, start int) (int, error) {
	cachedStart, ok := r.genStart(gen)
	if !ok || cachedStart != start {
		return 0, fmt.Errorf("%w: replica %q has no generation %d starting at %d",
			ErrUnknownGeneration, r.name, gen, start)
	}
	end := start
	for i := start; i < r.len(); i++ {
		if r.log[i].Gen == gen {
			end = i + 1
		} else {
			break
		}
	}
	return end, nil
}

// RecoverFollower 按世代多轮截断 follower 的分叉尾部（只截断，不拉取）。
func (c *Cluster) RecoverFollower(leader, follower string) (int, error) {
	if leader == follower {
		return 0, fmt.Errorf("%w: leader and follower must differ", ErrInvalidArgument)
	}
	rs, err := c.getMany([]string{leader, follower})
	if err != nil {
		return 0, err
	}
	unlock := lockReplicas(rs...)
	defer unlock()
	return recoverFollower(c, replicaByName(rs, leader), replicaByName(rs, follower))
}
