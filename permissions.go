package ontology

// 本文件实现「权限继承与覆盖解析」模块（Resolver）。
//
// 模型：
//   - 操作者（actor）可直接属于若干角色（role）。
//   - 角色之间允许声明 contains 包含关系：角色 A 包含角色 B 表示 B 是 A 的
//     上层（B 的成员“包含”A 的成员，即 B 的声明沿 B→A 方向向下继承）。
//     从操作者的角度，沿 actor -> 其所属角色 -> 被包含角色... 逐层向上可达，
//     到达某角色所需的最少边数即「层级距离」。
//   - 覆盖声明（grant）可挂在操作者自身（距离 0）或任意角色上，内容为某
//     实例上某链接来源属性对某操作者（或经由角色）的可见/不可见。
//   - 最终可见性：在操作者自身与沿层级可达的全部角色上声明的覆盖中，
//     取层级距离最短者；距离相等取声明时间更晚（seq 更大）者；
//     没有任何直接或间接覆盖时退回对象类型层默认值。
//
// 复杂度保证：
//   解析以「最近优先、命中即停」的逐层 BFS 完成：一旦在距离 d 找到覆盖，
//   绝不再访问距离 > d 的任何角色。因此角色层级总规模、系统角色总数的增长
//   不会改变解析工作量；遍历量只与「到最近覆盖的距离 d 之内的邻域」相关。
//   ResolveTrace 额外返回该次解析实际访问过的主体数，供测试直接核验。
//   Platform 进一步为每个 actor 缓存最近覆盖的来源与距离；两次声明变更
//   之间的反复查询为 O(1) 哈希命中，不触发任何层级遍历。

// grantSubject 标识一条覆盖声明的挂载主体（操作者或角色）。
type grantSubject struct {
	kind int // 0=actor, 1=role
	name string
}

// grantTarget 标识一条覆盖作用的「实例 + 链接来源属性」。
type grantTarget struct {
	instance string
	property string
}

// grantRecord 是一条覆盖声明。
type grantRecord struct {
	vis Visibility
	seq int64 // 单调递增的声明时间序，越大越晚
}

// resolveHit 描述一次解析命中的覆盖及其依据。
type resolveHit struct {
	vis      Visibility
	distance int
	seq      int64
	subject  string
}

// Resolver 负责操作者通过角色层级获得的属性可见权限的继承与覆盖解析。
// Resolver 的读写由持有它的 Platform 在互斥锁内完成，自身不再加锁，
// 朴素模型也复用同一套数据结构，保证两边「声明」完全等价。
type Resolver struct {
	// actorRoles: 操作者直接所属角色。
	actorRoles map[string]map[string]bool
	// parents[r]：角色 r 通过包含关系向上可达的直接父角色集合
	// （A contains B ⇒ B 是 A 的父，B 的声明被 A 的成员继承）。
	parents map[string]map[string]bool
	// grants[subject][target]：该主体在该实例属性上最新的覆盖声明。
	grants map[grantSubject]map[grantTarget]grantRecord
	// instanceType：实例 -> 对象类型，用于回退类型层默认值。
	instanceType map[string]string
	// typeDefault：对象类型 -> 属性 -> 默认可见性。
	typeDefault map[string]map[string]Visibility
	// 每个「实例属性」声明的最后 seq，用于该目标缓存失效判定。
	versionOf map[grantTarget]int64
	// actorVersion：角色/成员关系的结构版本，变更即整体清缓存。
	structureSeq int64
	// 解析次数计数，便于测试/演示观察缓存命中。
	lookups int64

	// cache[actor][target]：该操作者对该目标最近覆盖来源的缓存。
	cache map[string]map[grantTarget]resolveCacheEntry
}

type resolveCacheEntry struct {
	hit         resolveHit
	usedDefault bool
	structVer   int64
	grantVer    int64
}

// NewResolver 创建权限解析器。
func NewResolver() *Resolver {
	return &Resolver{
		actorRoles:   make(map[string]map[string]bool),
		parents:      make(map[string]map[string]bool),
		grants:       make(map[grantSubject]map[grantTarget]grantRecord),
		instanceType: make(map[string]string),
		typeDefault:  make(map[string]map[string]Visibility),
		versionOf:    make(map[grantTarget]int64),
		cache:        make(map[string]map[grantTarget]resolveCacheEntry),
	}
}

// SetTypeDefault 登记对象类型层的属性默认可见性。
func (r *Resolver) SetTypeDefault(objectType, property string, vis Visibility) {
	if r.typeDefault[objectType] == nil {
		r.typeDefault[objectType] = make(map[string]Visibility)
	}
	r.typeDefault[objectType][property] = vis
}

// SetInstanceType 登记实例所属对象类型。
func (r *Resolver) SetInstanceType(instance, objectType string) {
	r.instanceType[instance] = objectType
}

// AddActorRole 声明操作者直接属于某角色。
func (r *Resolver) AddActorRole(actor, role string) {
	if r.actorRoles[actor] == nil {
		r.actorRoles[actor] = make(map[string]bool)
	}
	if !r.actorRoles[actor][role] {
		r.actorRoles[actor][role] = true
		r.structureSeq++
		r.invalidateStructure()
	}
}

// AddRoleContains 声明容器角色 contains 被包含角色 inner：
// inner 是 contains 的上层，contains 的成员继承 inner 上的覆盖。
func (r *Resolver) AddRoleContains(contains, inner string) {
	if r.parents[contains] == nil {
		r.parents[contains] = make(map[string]bool)
	}
	if !r.parents[contains][inner] {
		r.parents[contains][inner] = true
		r.structureSeq++
		r.invalidateStructure()
	}
}

// GrantActor 在操作者自身（距离 0）上声明实例属性的覆盖。
func (r *Resolver) GrantActor(actor, instance, property string, vis Visibility, seq int64) {
	r.grant(grantSubject{0, actor}, grantTarget{instance, property}, vis, seq)
}

// GrantRole 在某角色上声明实例属性的覆盖（供其层级下所有操作者继承）。
func (r *Resolver) GrantRole(role, instance, property string, vis Visibility, seq int64) {
	r.grant(grantSubject{1, role}, grantTarget{instance, property}, vis, seq)
}

func (r *Resolver) grant(sub grantSubject, tgt grantTarget, vis Visibility, seq int64) {
	if r.grants[sub] == nil {
		r.grants[sub] = make(map[grantTarget]grantRecord)
	}
	// 只接受更晚的声明，避免重放/乱序场景覆盖更新被旧值回滚。
	if cur, ok := r.grants[sub][tgt]; ok && cur.seq >= seq {
		return
	}
	r.grants[sub][tgt] = grantRecord{vis: vis, seq: seq}
	if seq > r.versionOf[tgt] {
		r.versionOf[tgt] = seq
	}
	// 该目标上的缓存可能引用此主体，按目标作废。
	for actor := range r.cache {
		delete(r.cache[actor], tgt)
	}
}

func (r *Resolver) invalidateStructure() {
	r.cache = make(map[string]map[grantTarget]resolveCacheEntry)
}

// Visibility 解析操作者对实例上某链接来源属性的最终可见性（带 O(1) 缓存）。
func (r *Resolver) Visibility(actor, instance, property string) Visibility {
	r.lookups++
	tgt := grantTarget{instance, property}
	if m := r.cache[actor]; m != nil {
		if e, ok := m[tgt]; ok &&
			e.structVer == r.structureSeq && e.grantVer == r.versionOf[tgt] {
			return e.hit.vis
		}
	}
	hit, usedDefault, _ := r.findNearest(actor, tgt, false)
	r.putCache(actor, tgt, hit, usedDefault)
	return hit.vis
}

func (r *Resolver) putCache(actor string, tgt grantTarget, hit resolveHit, usedDefault bool) {
	if r.cache[actor] == nil {
		r.cache[actor] = make(map[grantTarget]resolveCacheEntry)
	}
	r.cache[actor][tgt] = resolveCacheEntry{
		hit:         hit,
		usedDefault: usedDefault,
		structVer:   r.structureSeq,
		grantVer:    r.versionOf[tgt],
	}
}

// ResolveTrace 执行一次不经过缓存的完整解析，并返回命中依据与实际访问的
// 主体（操作者 + 角色）数量，供复杂度验证使用：traversed 只随「到最近覆盖
// 的距离」变化，不随系统角色总数变化。
func (r *Resolver) ResolveTrace(actor, instance, property string) (hit resolveHit, usedDefault bool, traversed int) {
	return r.findNearest(actor, grantTarget{instance, property}, true)
}

// findNearest 用「距离优先、命中即停」的 BFS 找最近覆盖。
// trace=true 时统计实际访问主体数。
func (r *Resolver) findNearest(actor string, tgt grantTarget, trace bool) (resolveHit, bool, int) {
	traversed := 0
	visit := func(name string) {
		if trace {
			traversed++
		}
	}

	// 距离 0：操作者自身的直接声明。
	visit(actor)
	best, found := r.pick(grantSubject{0, actor}, tgt, actor, 0)
	if found {
		return best, false, traversed
	}

	// 距离 >=1：从直接所属角色出发逐层向上，同层取 seq 最大者，
	// 一旦某层命中即停止，绝不访问更远层。
	visited := map[string]bool{}
	current := []string{}
	for role := range r.actorRoles[actor] {
		if !visited[role] {
			visited[role] = true
			current = append(current, role)
		}
	}
	for distance := 1; len(current) > 0; distance++ {
		layerBest := resolveHit{}
		layerFound := false
		next := []string{}
		for _, role := range current {
			visit(role)
			if h, ok := r.pick(grantSubject{1, role}, tgt, role, distance); ok {
				if !layerFound || h.seq > layerBest.seq {
					layerBest, layerFound = h, true
				}
			}
			for parent := range r.parents[role] {
				if !visited[parent] {
					visited[parent] = true
					next = append(next, parent)
				}
			}
		}
		if layerFound {
			return layerBest, false, traversed
		}
		current = next
	}

	// 无任何直接或间接覆盖：退回类型层默认。
	return resolveHit{vis: r.defaultOf(tgt), distance: -1, subject: "<type-default>"}, true, traversed
}

func (r *Resolver) pick(sub grantSubject, tgt grantTarget, name string, distance int) (resolveHit, bool) {
	if m := r.grants[sub]; m != nil {
		if g, ok := m[tgt]; ok {
			return resolveHit{vis: g.vis, distance: distance, seq: g.seq, subject: name}, true
		}
	}
	return resolveHit{}, false
}

func (r *Resolver) defaultOf(tgt grantTarget) Visibility {
	t := r.instanceType[tgt.instance]
	if m := r.typeDefault[t]; m != nil {
		if v, ok := m[tgt.property]; ok {
			return v
		}
	}
	return Invisible
}

// CacheHitRate 仅供演示/测试观察：返回已执行查询数（缓存命中不触发遍历）。
func (r *Resolver) Lookups() int64 { return r.lookups }
