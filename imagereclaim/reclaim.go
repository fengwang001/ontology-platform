package imagereclaim

import (
	"sort"
	"sync"
)

// Layer 描述镜像的一个有序层。
type Layer struct {
	ID   string
	Size int64
}

// ErrCode 标识操作被拒绝的原因。
type ErrCode int

const (
	ErrConfigInvalid ErrCode = iota + 1
	ErrInvalidParam
	ErrClockRewind
	ErrImageExists
	ErrImageNotFound
	ErrImagePulling
	ErrImageReady
	ErrLayerConflict
	ErrNoSpace
	ErrNotRunning
)

// Error 记录一次被拒绝操作的原因码，层冲突时附带层 ID。
type Error struct {
	Code    ErrCode
	LayerID string
}

func (e *Error) Error() string { return "imagereclaim: rejected" }

// GCResult 是一次 GC 的结果。
type GCResult struct {
	Deleted []string // 被删除的镜像 ID，按删除先后排列
	Freed   int64    // 本次删除累计释放的层字节数
	Short   bool     // 候选用尽仍未达到回收目标时为真
}

type imageState int

const (
	statePulling imageState = iota
	stateReady
)

type imageInfo struct {
	layers   []Layer // 有序层列表（拉取登记后不再变更）
	state    imageState
	lastUsed int64
	run      int
}

type layerInfo struct {
	size int64
	refs int
}

// Reclaimer 是节点镜像磁盘回收器；所有方法可并发调用，语义等价于某一串行顺序。
type Reclaimer struct {
	mu       sync.Mutex
	capacity int64
	keep     int
	used     int64
	maxNow   int64
	images   map[string]*imageInfo
	layers   map[string]*layerInfo
}

// New 构造回收器；C 为磁盘容量（字节），K 为每仓库保护镜像数。
func New(capacity int64, keepPerRepo int) (*Reclaimer, error) {
	if capacity < 1 || capacity > 1_000_000_000_000_000 || keepPerRepo < 0 || keepPerRepo > 100 {
		return nil, &Error{Code: ErrConfigInvalid}
	}
	return &Reclaimer{
		capacity: capacity,
		keep:     keepPerRepo,
		images:   make(map[string]*imageInfo),
		layers:   make(map[string]*layerInfo),
	}, nil
}

// Used 返回当前磁盘占用。
func (r *Reclaimer) Used() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.used
}

func reject(code ErrCode, layerID string) error {
	return &Error{Code: code, LayerID: layerID}
}

func validateLayers(img string, layers []Layer) error {
	if img == "" || len(layers) == 0 {
		return reject(ErrInvalidParam, "")
	}
	seen := make(map[string]struct{}, len(layers))
	for _, l := range layers {
		if l.ID == "" || l.Size < 1 || l.Size > 1_000_000_000_000 {
			return reject(ErrInvalidParam, "")
		}
		if _, dup := seen[l.ID]; dup {
			return reject(ErrInvalidParam, "")
		}
		seen[l.ID] = struct{}{}
	}
	return nil
}

func (r *Reclaimer) checkClock(now int64) error {
	if now < 0 {
		return reject(ErrInvalidParam, "")
	}
	if now < r.maxNow {
		return reject(ErrClockRewind, "")
	}
	return nil
}

// beginPullLocked 执行两阶段拉取的第一阶段；调用方需持锁，成功后状态已落盘。
func (r *Reclaimer) beginPullLocked(img string, layers []Layer, now int64) error {
	if err := validateLayers(img, layers); err != nil {
		return err
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	if _, exists := r.images[img]; exists {
		return reject(ErrImageExists, "")
	}
	var add int64
	for _, l := range layers {
		if ex, ok := r.layers[l.ID]; ok {
			if ex.size != l.Size {
				return reject(ErrLayerConflict, l.ID)
			}
			continue
		}
		add += l.Size
	}
	if r.used > r.capacity || add > r.capacity-r.used {
		return reject(ErrNoSpace, "")
	}

	stored := make([]Layer, len(layers))
	copy(stored, layers)
	info := &imageInfo{layers: stored, state: statePulling}
	r.images[img] = info
	for _, l := range layers {
		ex := r.layers[l.ID]
		if ex == nil {
			r.layers[l.ID] = &layerInfo{size: l.Size, refs: 1}
			continue
		}
		ex.refs++
	}
	r.used += add
	r.maxNow = now
	return nil
}

// BeginPull 登记一个拉取中的镜像并立即占用其新增层。
func (r *Reclaimer) BeginPull(img string, layers []Layer, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.beginPullLocked(img, layers, now)
}

// CommitPull 使拉取中的镜像变为就绪。
func (r *Reclaimer) CommitPull(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if img == "" || now < 0 {
		return reject(ErrInvalidParam, "")
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	info := r.images[img]
	if info == nil {
		return reject(ErrImageNotFound, "")
	}
	if info.state != statePulling {
		return reject(ErrImageReady, "")
	}
	info.state = stateReady
	info.lastUsed = now
	info.run = 0
	r.maxNow = now
	return nil
}

// removeImageLocked 删除一个镜像并释放引用归零的层，返回释放字节数。
func (r *Reclaimer) removeImageLocked(info *imageInfo) int64 {
	var freed int64
	for _, l := range info.layers {
		ex := r.layers[l.ID]
		ex.refs--
		if ex.refs == 0 {
			freed += ex.size
			delete(r.layers, l.ID)
		}
	}
	r.used -= freed
	return freed
}

// AbortPull 放弃拉取中的镜像并释放独占层。
func (r *Reclaimer) AbortPull(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if img == "" || now < 0 {
		return reject(ErrInvalidParam, "")
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	info := r.images[img]
	if info == nil {
		return reject(ErrImageNotFound, "")
	}
	if info.state != statePulling {
		return reject(ErrImageReady, "")
	}
	r.removeImageLocked(info)
	delete(r.images, img)
	r.maxNow = now
	return nil
}

// Pull 原子地完成 BeginPull 与 CommitPull。
func (r *Reclaimer) Pull(img string, layers []Layer, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.beginPullLocked(img, layers, now); err != nil {
		return err
	}
	info := r.images[img]
	info.state = stateReady
	info.lastUsed = now
	info.run = 0
	return nil
}

// Run 将就绪镜像的运行数加一。
func (r *Reclaimer) Run(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if img == "" || now < 0 {
		return reject(ErrInvalidParam, "")
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	info := r.images[img]
	if info == nil {
		return reject(ErrImageNotFound, "")
	}
	if info.state == statePulling {
		return reject(ErrImagePulling, "")
	}
	info.run++
	info.lastUsed = now
	r.maxNow = now
	return nil
}

// Stop 将就绪镜像的运行数减一。
func (r *Reclaimer) Stop(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if img == "" || now < 0 {
		return reject(ErrInvalidParam, "")
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	info := r.images[img]
	if info == nil {
		return reject(ErrImageNotFound, "")
	}
	if info.state == statePulling {
		return reject(ErrImagePulling, "")
	}
	if info.run == 0 {
		return reject(ErrNotRunning, "")
	}
	info.run--
	info.lastUsed = now
	r.maxNow = now
	return nil
}

// GC 按保护与水位规则回收镜像层。
func (r *Reclaimer) GC(now int64, high, low int, minAge int64) (GCResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if high <= 0 || high > 100 || low <= 0 || low >= high || minAge < 0 || now < 0 {
		return GCResult{}, reject(ErrInvalidParam, "")
	}
	if err := r.checkClock(now); err != nil {
		return GCResult{}, err
	}
	r.maxNow = now

	used := r.used
	usedPct := used * 100
	threshold := int64(high) * r.capacity
	if usedPct < threshold {
		return GCResult{Deleted: nil, Freed: 0, Short: false}, nil
	}
	target := used - (r.capacity*int64(low))/100

	// 保护集在 GC 开始时一次算出：每仓库就绪镜像按 lastUsed 降序、
	// lastUsed 相同按镜像 ID 升序排列，前 K 个受保护。
	byRepo := make(map[string][]string)
	for id, info := range r.images {
		if info.state == stateReady {
			byRepo[repoOf(id)] = append(byRepo[repoOf(id)], id)
		}
	}
	protected := make(map[string]bool)
	for _, ids := range byRepo {
		sort.Slice(ids, func(i, j int) bool {
			a, b := r.images[ids[i]], r.images[ids[j]]
			if a.lastUsed != b.lastUsed {
				return a.lastUsed > b.lastUsed
			}
			return ids[i] < ids[j]
		})
		limit := r.keep
		if limit > len(ids) {
			limit = len(ids)
		}
		for _, id := range ids[:limit] {
			protected[id] = true
		}
	}

	// 候选：就绪、run==0、未受保护且年龄达到 minAge；按 lastUsed 升序、
	// lastUsed 相同按镜像 ID 升序逐个删除。
	var candidates []string
	for id, info := range r.images {
		if info.state != stateReady || info.run != 0 || protected[id] {
			continue
		}
		if now-info.lastUsed < minAge {
			continue
		}
		candidates = append(candidates, id)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := r.images[candidates[i]], r.images[candidates[j]]
		if a.lastUsed != b.lastUsed {
			return a.lastUsed < b.lastUsed
		}
		return candidates[i] < candidates[j]
	})

	result := GCResult{}
	for _, id := range candidates {
		info := r.images[id]
		freed := r.removeImageLocked(info)
		delete(r.images, id)
		result.Deleted = append(result.Deleted, id)
		result.Freed += freed
		if result.Freed >= target {
			result.Short = false
			return result, nil
		}
	}
	result.Short = result.Freed < target
	return result, nil
}

// repoOf 返回镜像 ID 的仓库名：最后一个 ":" 之前的部分，无 ":" 时为整个 ID。
func repoOf(img string) string {
	for i := len(img) - 1; i >= 0; i-- {
		if img[i] == ':' {
			return img[:i]
		}
	}
	return img
}
