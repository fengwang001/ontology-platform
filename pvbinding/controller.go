package pvbinding

import (
	"sort"
	"sync"
)

// Controller 是持久卷与卷声明的绑定控制器。
type Controller struct {
	mu sync.Mutex

	vols   map[string]*Volume
	claims map[string]*Claim

	// avail 是所有 Available 卷按存储类的有序索引；Released/Bound 卷不在其中。
	avail *volumeIndex

	filter candidateFilter

	// examinedTotal 累计立即绑定选卷时实际检查过的同存储类卷数。
	examinedTotal int
	// examinedOps 累计触发过选卷扫描的次数。
	examinedOps int
}

// New 创建空控制器。
func New() *Controller {
	return &Controller{
		vols:   make(map[string]*Volume),
		claims: make(map[string]*Claim),
		avail:  newVolumeIndexData(),
	}
}

func cloneVolumeSpec(s VolumeSpec) VolumeSpec {
	return VolumeSpec{
		Capacity:      s.Capacity,
		StorageClass:  s.StorageClass,
		AccessModes:   cloneAccessModes(s.AccessModes),
		Labels:        cloneStringMap(s.Labels),
		NodeNames:     cloneStringSet(s.NodeNames),
		ReservedClaim: s.ReservedClaim,
		Reclaim:       s.Reclaim,
	}
}

func cloneClaimSpec(s ClaimSpec) ClaimSpec {
	return ClaimSpec{
		RequestCapacity: s.RequestCapacity,
		StorageClass:    s.StorageClass,
		AccessModes:     cloneAccessModes(s.AccessModes),
		Selector:        cloneStringMap(s.Selector),
		VolumeName:      s.VolumeName,
		BindMode:        s.BindMode,
	}
}

// AddVolume 添加卷。
// 若卷的 ReservedClaim 指向已存在的待绑定声明，会立即尝试形成预绑定。
func (c *Controller) AddVolume(name string, spec VolumeSpec) error {
	if err := validateVolumeSpec(name, spec); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.vols[name]; ok {
		return &ControllerError{Kind: ErrConflict, Op: "AddVolume", Detail: "volume already exists: " + name}
	}
	v := &Volume{Name: name, Spec: cloneVolumeSpec(spec), Phase: VolumeAvailable,
		ClaimName: spec.ReservedClaim}
	c.vols[name] = v
	c.avail.add(v)
	c.reevaluateLocked()
	return nil
}

// AddClaim 添加声明。立即绑定模式会当场尝试选卷；延迟绑定模式保持待绑定。
func (c *Controller) AddClaim(name string, spec ClaimSpec) error {
	if err := validateClaimSpec(name, spec); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.claims[name]; ok {
		return &ControllerError{Kind: ErrConflict, Op: "AddClaim", Detail: "claim already exists: " + name}
	}
	cl := &Claim{Name: name, Spec: cloneClaimSpec(spec)}
	c.claims[name] = cl
	if cl.Spec.BindMode == BindImmediate {
		c.reevaluateLocked()
	}
	return nil
}

// UpdateVolume 修改 Available 卷的规格，随后触发待绑定立即声明的重新评估。
// Bound/Released 卷不允许修改（状态冲突）；不存在的卷返回对象不存在。
func (c *Controller) UpdateVolume(name string, spec VolumeSpec) error {
	if err := validateVolumeSpec(name, spec); err != nil {
		// 校验错误的操作名归一为 UpdateVolume。
		if ce, ok := err.(*ControllerError); ok {
			ce.Op = "UpdateVolume"
		}
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.vols[name]
	if !ok {
		return &ControllerError{Kind: ErrNotFound, Op: "UpdateVolume", Detail: "volume not found: " + name}
	}
	if v.Phase != VolumeAvailable {
		return &ControllerError{Kind: ErrConflict, Op: "UpdateVolume",
			Detail: "only available volumes can be updated: " + name}
	}
	c.avail.remove(v)
	v.Spec = cloneVolumeSpec(spec)
	v.ClaimName = spec.ReservedClaim
	c.avail.add(v)
	c.reevaluateLocked()
	return nil
}

// DeleteClaim 删除声明。
// 已绑定：卷进入回收（Delete 移除；Retain 转 Released）；待绑定：不影响任何卷。
func (c *Controller) DeleteClaim(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl, ok := c.claims[name]
	if !ok {
		return &ControllerError{Kind: ErrNotFound, Op: "DeleteClaim", Detail: "claim not found: " + name}
	}
	if cl.Bound {
		v := c.vols[cl.VolumeName]
		c.avail.remove(v)
		if v.Spec.Reclaim == ReclaimDelete {
			delete(c.vols, v.Name)
		} else {
			v.Phase = VolumeReleased
			v.ClaimName = ""
			v.Spec.ReservedClaim = ""
		}
	}
	delete(c.claims, name)
	if cl.Bound {
		c.reevaluateLocked()
	}
	return nil
}

// ReleaseVolume 是管理员操作：将 Released 卷重置为 Available（清除历史身份）。
func (c *Controller) ResetVolume(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.vols[name]
	if !ok {
		return &ControllerError{Kind: ErrNotFound, Op: "ResetVolume", Detail: "volume not found: " + name}
	}
	if v.Phase != VolumeReleased {
		return &ControllerError{Kind: ErrConflict, Op: "ResetVolume", Detail: "volume is not Released: " + name}
	}
	v.Phase = VolumeAvailable
	v.ClaimName = ""
	c.avail.add(v)
	c.reevaluateLocked()
	return nil
}

// ExpandClaim 提高已绑定声明的请求容量，不得超过所绑卷容量，也不允许降低。
func (c *Controller) ExpandClaim(name string, newCapacity int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if newCapacity <= 0 {
		return &ControllerError{Kind: ErrInvalidArgument, Op: "ExpandClaim", Detail: "new capacity must be positive"}
	}
	cl, ok := c.claims[name]
	if !ok {
		return &ControllerError{Kind: ErrNotFound, Op: "ExpandClaim", Detail: "claim not found: " + name}
	}
	if !cl.Bound {
		return &ControllerError{Kind: ErrConflict, Op: "ExpandClaim", Detail: "claim is not bound: " + name}
	}
	if newCapacity < cl.Spec.RequestCapacity {
		return &ControllerError{Kind: ErrInvalidArgument, Op: "ExpandClaim", Detail: "capacity reduction is not allowed"}
	}
	v := c.vols[cl.VolumeName]
	if newCapacity > v.Spec.Capacity {
		return &ControllerError{Kind: ErrCapacityExceeded, Op: "ExpandClaim",
			Detail: "requested capacity exceeds bound volume capacity"}
	}
	cl.Spec.RequestCapacity = newCapacity
	return nil
}

// JointBindRequest 是一次延迟联合绑定的调用参数。
type JointBindRequest struct {
	Node       string
	ClaimNames []string
}

// JointBind 在给定节点上为一组延迟声明做全有或全无的联合指派。
func (c *Controller) JointBind(req JointBindRequest) ([]Assignment, error) {
	const op = "JointBind"
	if req.Node == "" {
		return nil, &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "node name is empty"}
	}
	if len(req.ClaimNames) == 0 {
		return nil, &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "claim list is empty"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	names := append([]string(nil), req.ClaimNames...)
	sort.Strings(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			return nil, &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "duplicate claim: " + names[i]}
		}
	}

	refs := make([]*Claim, 0, len(names))
	var firstMissing string
	for _, n := range names {
		cl, ok := c.claims[n]
		if !ok {
			if firstMissing == "" {
				firstMissing = n
			}
			continue
		}
		refs = append(refs, cl)
	}
	// 参数非法优先于对象不存在：先判定所有已存在声明的模式是否合法。
	for _, cl := range refs {
		if cl.Spec.BindMode != BindWaitForConsumer {
			return nil, &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "claim is not delayed: " + cl.Name}
		}
	}
	if firstMissing != "" {
		return nil, &ControllerError{Kind: ErrNotFound, Op: op, Detail: "claim not found: " + firstMissing}
	}
	// 对象存在后再判定状态冲突。
	for _, cl := range refs {
		if cl.Bound {
			return nil, &ControllerError{Kind: ErrConflict, Op: op, Detail: "claim already bound: " + cl.Name}
		}
	}
	claims := refs

	candidates := make([][]*Volume, len(claims))
	for i, cl := range claims {
		var list []*Volume
		for _, v := range c.avail.bucket(cl.Spec.StorageClass) {
			if c.filter.matches(v, cl) && nodeAllowed(v, req.Node) {
				list = append(list, v)
			}
		}
		if len(list) == 0 {
			return nil, &ControllerError{Kind: ErrNoMatch, Op: op,
				Detail: "no joint assignment exists for claim: " + cl.Name}
		}
		candidates[i] = list
	}

	solution := jointBind(claims, candidates)
	if solution == nil {
		return nil, &ControllerError{Kind: ErrNoMatch, Op: op, Detail: "no joint assignment exists"}
	}

	result := make([]Assignment, 0, len(claims))
	for i, cl := range claims {
		v := solution[i]
		c.bindLocked(cl, v)
		result = append(result, Assignment{ClaimName: cl.Name, VolumeName: v.Name})
	}
	return result, nil
}

// bindLocked 在锁内建立声明与卷的互相一致绑定。
func (c *Controller) bindLocked(cl *Claim, v *Volume) {
	c.avail.remove(v)
	v.Phase = VolumeBound
	v.ClaimName = cl.Name
	v.Spec.ReservedClaim = ""
	cl.Bound = true
	cl.VolumeName = v.Name
}

// reevaluateLocked 按声明名称升序，对所有待绑定的立即模式声明重新选卷。
// 触发时机：新卷加入、卷被释放（删除绑定声明）、Released 卷被重置。
func (c *Controller) reevaluateLocked() {
	names := make([]string, 0, len(c.claims))
	for n, cl := range c.claims {
		if !cl.Bound && cl.Spec.BindMode == BindImmediate {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		cl := c.claims[n]
		if cl == nil || cl.Bound {
			continue
		}
		c.examinedOps++
		v, examined := c.avail.pickImmediate(cl, c.filter)
		c.examinedTotal += examined
		if v != nil {
			c.bindLocked(cl, v)
		}
	}
}

// Snapshot 返回全量状态的深拷贝。
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := Snapshot{
		Volumes: make(map[string]*Volume, len(c.vols)),
		Claims:  make(map[string]*Claim, len(c.claims)),
	}
	for n, v := range c.vols {
		snap.Volumes[n] = &Volume{
			Name:      v.Name,
			Spec:      cloneVolumeSpec(v.Spec),
			Phase:     v.Phase,
			ClaimName: v.ClaimName,
		}
	}
	for n, cl := range c.claims {
		snap.Claims[n] = &Claim{
			Name:       cl.Name,
			Spec:       cloneClaimSpec(cl.Spec),
			Bound:      cl.Bound,
			VolumeName: cl.VolumeName,
		}
	}
	return snap
}

// Stats 是选卷观测数据，用于性能可验证。
type Stats struct {
	ExaminedTotal int
	ExaminedOps   int
	AvailableBySC map[string]int
}

// Stats 返回选卷扫描累计计数与各存储类可用卷数量。
func (c *Controller) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := make(map[string]int)
	for sc := range c.allStorageClassesLocked() {
		m[sc] = c.avail.bucketSize(sc)
	}
	return Stats{ExaminedTotal: c.examinedTotal, ExaminedOps: c.examinedOps, AvailableBySC: m}
}

func (c *Controller) allStorageClassesLocked() map[string]struct{} {
	set := make(map[string]struct{})
	for _, v := range c.vols {
		set[v.Spec.StorageClass] = struct{}{}
	}
	for _, cl := range c.claims {
		set[cl.Spec.StorageClass] = struct{}{}
	}
	return set
}
