package replication

import (
	"log"
	"sync"
)

// Cluster 管理一组副本，提供世代响应、截断与恢复复制。
// 所有方法均可被多个执行体并发调用。
type Cluster struct {
	mu       sync.Mutex // 保护 replicas 表本身
	replicas map[string]*Replica
}

// NewCluster 创建空集群。
func NewCluster() *Cluster {
	return &Cluster{replicas: map[string]*Replica{}}
}

// AddReplica 登记一个已有日志与世代缓存的副本。
// 日志条目世代必须不减且都存在于世代缓存中；缓存项世代、起始位点必须严格递增。
func (c *Cluster) AddReplica(id string, entries []LogEntry, epochs []EpochMark) error {
	if id == "" {
		return errInvalidArgument
	}
	if err := validateLog(entries, epochs); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.replicas[id]; dup {
		return errInvalidArgument
	}
	r := &Replica{id: id}
	r.log = append(r.log, entries...)
	r.epochs = append(r.epochs, epochs...)
	c.replicas[id] = r
	return nil
}

func validateLog(entries []LogEntry, epochs []EpochMark) error {
	known := map[int]bool{}
	prevEpoch, prevStart := -1, -1
	for _, m := range epochs {
		if m.Epoch <= prevEpoch || m.Start <= prevStart || m.Start < 0 || m.Start > len(entries) {
			return errInvalidArgument
		}
		prevEpoch, prevStart = m.Epoch, m.Start
		known[m.Epoch] = true
	}
	prev := -1
	for _, e := range entries {
		if e.Epoch < prev || !known[e.Epoch] {
			return errInvalidArgument
		}
		prev = e.Epoch
	}
	return nil
}

// Replica 按名取副本。
func (c *Cluster) Replica(id string) *Replica {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.replicas[id]
}

func (c *Cluster) mustGet(id string) (*Replica, error) {
	if id == "" {
		return nil, errInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.replicas[id]
	if !ok {
		return nil, errUnknownReplica
	}
	return r, nil
}

// BecomeLeader 副本在当前世代当选，向世代缓存追加起始项。
func (c *Cluster) BecomeLeader(leaderID string, epoch int) error {
	r, err := c.mustGet(leaderID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if epoch <= r.lastEpoch() {
		return errInvalidArgument
	}
	r.elect(epoch)
	log.Printf("[elect] leader=%s epoch=%d start=%d 依据: 新世代大于当前最新世代，登记起始位点",
		leaderID, epoch, len(r.log))
	return nil
}

// AppendLeader 领导者以给定世代追加一条消息（新世代时追加缓存项）。
func (c *Cluster) AppendLeader(leaderID string, entry LogEntry) error {
	r, err := c.mustGet(leaderID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry.Leader != leaderID || entry.Epoch < r.lastEpoch() {
		return errInvalidArgument
	}
	r.appendEntry(entry)
	log.Printf("[append] leader=%s offset=%d epoch=%d id=%d 依据: 世代不小于最新世代，直接追加",
		leaderID, len(r.log)-1, entry.Epoch, entry.ID)
	return nil
}

// EpochResponse 领导者按请求世代返回该世代在领导者日志上的可用结束位点。
// 规则：世代在领导者缓存中时返回其结束位点（下一世代起始位点，
// 没有更大世代时为领导者日志结束位点）且 known=true；
// 世代超出领导者缓存范围（更新或落在空洞中）时 known=false，
// 跟随者应据此截掉整个未知世代；世代早于领导者最早缓存世代时报 ErrUnknownEpoch。
func (c *Cluster) EpochResponse(leaderID string, epoch int) (end int, known bool, err error) {
	r, err := c.mustGet(leaderID)
	if err != nil {
		return 0, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	end, known, err = epochEnd(r.log, r.epochs, epoch)
	if err != nil {
		return 0, false, err
	}
	log.Printf("[epoch-response] leader=%s reqEpoch=%d end=%d known=%v 依据: 下一世代起始位点或日志末尾",
		leaderID, epoch, end, known)
	return end, known, nil
}

// epochEnd 是纯函数：世代在缓存中时返回其结束位点，known=true；
// 世代比最新新或落在空洞中时 known=false；世代早于最早缓存项时报错。
func epochEnd(lLog []LogEntry, lEpochs []EpochMark, epoch int) (int, bool, error) {
	if len(lEpochs) == 0 || epoch < lEpochs[0].Epoch {
		return 0, false, errUnknownEpoch
	}
	for i, m := range lEpochs {
		if m.Epoch == epoch {
			if i+1 < len(lEpochs) {
				return lEpochs[i+1].Start, true, nil
			}
			return len(lLog), true, nil
		}
	}
	return 0, false, nil
}

// truncateState 纯函数：日志截到 cut，并删除起始位点 >= cut 的缓存项。
func truncateState(fLog []LogEntry, fEpochs []EpochMark, cut int) ([]LogEntry, []EpochMark) {
	keep := 0
	for keep < len(fEpochs) && fEpochs[keep].Start < cut {
		keep++
	}
	return fLog[:cut], fEpochs[:keep]
}

// roundStep 纯函数：执行一轮截断，返回新日志、新缓存、截断点与是否有变化。
// 世代已知时截断点 = min(世代响应位点, 跟随者结束位点)；
// 世代未知于领导者时截断点 = 该未知世代的起始位点。
func roundStep(lLog []LogEntry, lEpochs []EpochMark, fLog []LogEntry, fEpochs []EpochMark) ([]LogEntry, []EpochMark, int, bool, error) {
	if len(fEpochs) == 0 {
		return fLog, fEpochs, len(fLog), false, nil
	}
	reqEpoch := fEpochs[len(fEpochs)-1].Epoch
	end, known, err := epochEnd(lLog, lEpochs, reqEpoch)
	if err != nil {
		return nil, nil, 0, false, err
	}
	cut := 0
	if known {
		cut = end
		if len(fLog) < cut {
			cut = len(fLog)
		}
	} else {
		cut = fEpochs[len(fEpochs)-1].Start
	}
	changed := cut < len(fLog) || fEpochs[len(fEpochs)-1].Start >= cut
	if !changed {
		return fLog, fEpochs, cut, false, nil
	}
	newLog, newEpochs := truncateState(fLog, fEpochs, cut)
	log.Printf("[round] reqEpoch=%d known=%v leaderEnd=%d cut=%d 依据: 已知取 min(响应位点, 结束位点)，未知取该世代起始位点",
		reqEpoch, known, end, cut)
	return newLog, newEpochs, cut, true, nil
}

// TruncateRound 跟随者执行一轮截断，返回本轮截断点与日志是否变短。只截断不拉取。
func (c *Cluster) TruncateRound(leaderID, followerID string) (int, bool, error) {
	leader, follower, err := c.pair(leaderID, followerID)
	if err != nil {
		return 0, false, err
	}
	unlock := lockPair(leader, follower)
	defer unlock()

	newLog, newEpochs, cut, changed, err := roundStep(leader.log, leader.epochs, follower.log, follower.epochs)
	if err != nil {
		return 0, false, err
	}
	truncated := cut < len(follower.log)
	if changed {
		follower.log, follower.epochs = newLog, newEpochs
	}
	log.Printf("[truncate-round] leader=%s follower=%s cut=%d truncated=%v", leaderID, followerID, cut, truncated)
	return cut, truncated, nil
}

// Recover 截断到最长公共前缀后把领导者尾部复制给跟随者，幂等。
// 先在状态副本上模拟多轮截断并校验公共前缀，任何拒绝都不改变任何副本状态。
func (c *Cluster) Recover(leaderID, followerID string) (int, int, error) {
	leader, follower, err := c.pair(leaderID, followerID)
	if err != nil {
		return 0, 0, err
	}
	unlock := lockPair(leader, follower)
	defer unlock()

	// 模拟多轮截断：失败不落盘。
	fLog, fEpochs := follower.log, follower.epochs
	for {
		newLog, newEpochs, cut, changed, err := roundStep(leader.log, leader.epochs, fLog, fEpochs)
		if err != nil {
			log.Printf("[recover] leader=%s follower=%s 拒绝: %v", leaderID, followerID, err)
			return 0, 0, err
		}
		fLog, fEpochs = newLog, newEpochs
		log.Printf("[recover-round] leader=%s follower=%s cut=%d changed=%v", leaderID, followerID, cut, changed)
		if !changed {
			break
		}
	}
	cut := len(fLog)
	if commonPrefix(leader.log, fLog) != cut {
		at := commonPrefix(leader.log, fLog)
		log.Printf("[recover] leader=%s follower=%s 拒绝: 位点 %d 处条目分叉 leader=%+v follower=%+v",
			leaderID, followerID, at, leader.log[at], fLog[at])
		return 0, 0, errLogDiverged
	}

	// 校验通过，应用截断结果。
	follower.log, follower.epochs = fLog, fEpochs

	// 追平：把领导者尾部复制给跟随者。
	copied := 0
	for _, e := range leader.log[cut:] {
		follower.appendEntry(e)
		copied++
	}
	log.Printf("[recover] leader=%s follower=%s cut=%d copied=%d 依据: 截断到最长公共前缀后复制领导者尾部",
		leaderID, followerID, cut, copied)
	return cut, copied, nil
}

func (c *Cluster) pair(leaderID, followerID string) (*Replica, *Replica, error) {
	if leaderID == followerID {
		return nil, nil, errInvalidArgument
	}
	leader, err := c.mustGet(leaderID)
	if err != nil {
		return nil, nil, err
	}
	follower, err := c.mustGet(followerID)
	if err != nil {
		return nil, nil, err
	}
	return leader, follower, nil
}

// lockPair 按 id 字典序加锁，避免并发恢复时死锁。
func lockPair(a, b *Replica) func() {
	if a.id > b.id {
		a, b = b, a
	}
	a.mu.Lock()
	b.mu.Lock()
	return func() {
		b.mu.Unlock()
		a.mu.Unlock()
	}
}

// commonPrefix 返回两条日志的朴素最长公共前缀长度。
func commonPrefix(a, b []LogEntry) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// CommonPrefix 返回两条日志的朴素最长公共前缀长度（供测试校验截断点）。
func CommonPrefix(a, b []LogEntry) int { return commonPrefix(a, b) }
