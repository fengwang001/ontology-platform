package ontology

// override 是一条实例层覆盖声明。
type override struct {
	kind SubjectKind
	// subject 为操作者 ID（kind==SubjectOperator 时与操作者相同）或角色 ID。
	subject string
	vis     Visibility
	// seq 是声明在全局串行序列中的序号；同层级距离时取 seq 最大者（最新）。
	seq int
}

type overrideKey struct {
	instance string
	attr     string
	subject  string
}

// Resolver 负责权限继承与覆盖解析，不感知链接与基数。
type Resolver struct {
	// roles 记录所有已创建的角色（创建动作本身只是声明，便于校验非法引用）。
	roles map[string]bool
	// parents[child] = 该角色包含的父角色集合，形成有向层级，允许环。
	parents map[string]map[string]bool
	// memberships[operator] = 直接赋予的角色集合。
	memberships map[string]map[string]bool
	// overrides[key] = 该主体在该实例属性上最新的覆盖（重声明即替换并更新 seq）。
	overrides map[overrideKey]override
	// seq 提供单调递增的声明时间次序，由仲裁器集中发放。
	seq int

	// probe 不为 nil 时，resolve 会把实际访问的角色/声明数计入其中，
	// 作为“开销只随最近覆盖距离增长”的可验证证据。
	probe *Probe
}

// Probe 统计一次可见性解析实际触碰的图规模。
type Probe struct {
	// RolesVisited 是本次解析实际展开的角色节点数。
	RolesVisited int
	// OverridesChecked 是实际检查过的覆盖声明数（每个被访问主体至多 1 次）。
	OverridesChecked int
}

// NewResolver 创建空解析器。
func NewResolver() *Resolver {
	return &Resolver{
		roles:       map[string]bool{},
		parents:     map[string]map[string]bool{},
		memberships: map[string]map[string]bool{},
		overrides:   map[overrideKey]override{},
	}
}

func (r *Resolver) addRole(role string) bool {
	if role == "" || r.roles[role] {
		return false
	}
	r.roles[role] = true
	return true
}

func (r *Resolver) includeRole(child, parent string) bool {
	if child == "" || parent == "" || child == parent {
		return false
	}
	if !r.roles[child] || !r.roles[parent] {
		return false
	}
	if r.parents[child] == nil {
		r.parents[child] = map[string]bool{}
	}
	if r.parents[child][parent] {
		return false
	}
	r.parents[child][parent] = true
	return true
}

func (r *Resolver) assignRole(operator, role string) bool {
	if operator == "" || role == "" || !r.roles[role] {
		return false
	}
	if r.memberships[operator] == nil {
		r.memberships[operator] = map[string]bool{}
	}
	if r.memberships[operator][role] {
		return false
	}
	r.memberships[operator][role] = true
	return true
}

func (r *Resolver) declareOverride(instanceID, attr string, kind SubjectKind, subject string, vis Visibility, seq int) bool {
	if instanceID == "" || attr == "" || subject == "" {
		return false
	}
	if kind == SubjectRole && !r.roles[subject] {
		return false
	}
	r.overrides[overrideKey{instanceID, attr, subject}] = override{
		kind: kind, subject: subject, vis: vis, seq: seq,
	}
	return true
}

// resolve 计算 operator 对 (instanceID, attr) 的最终可见性与判定依据。
// fallback 为没有任何可达覆盖时退回的类型层默认。
//
// 解析过程是按层级距离从短到长的逐层扩展：距离 0 只看操作者直接声明，
// 距离 d 看沿“直接赋予角色 → 角色包含链”恰好走 d 步可到达的角色。
// 一旦在某一层发现覆盖即停止，绝不展开更远的层级，也从不枚举系统中
// 与该操作者不可达的角色，因此开销只与“到最近覆盖的距离”相关。
func (r *Resolver) resolve(operator, instanceID, attr string, fallback Visibility) VisBasis {
	best, found, dist := r.nearestOverride(operator, instanceID, attr)
	if !found {
		return VisBasis{
			InstanceID: instanceID,
			Attr:       attr,
			Operator:   operator,
			Vis:        fallback,
			Distance:   -1,
			Source:     "TYPE_DEFAULT",
		}
	}
	return VisBasis{
		InstanceID: instanceID,
		Attr:       attr,
		Operator:   operator,
		Vis:        best.vis,
		Distance:   dist,
		Source:     best.kind.String() + ":" + best.subject,
		Seq:        best.seq,
	}
}

// nearestOverride 执行带探针的逐层 BFS，返回最近一层中声明序号最新的覆盖。
func (r *Resolver) nearestOverride(operator, instanceID, attr string) (override, bool, int) {
	key := overrideKey{instance: instanceID, attr: attr}
	var empty override

	// 距离 0：操作者本人。
	if r.probe != nil {
		r.probe.OverridesChecked++
	}
	if ov, ok := r.overrides[overrideKey{instanceID, attr, operator}]; ok {
		return ov, true, 0
	}

	// BFS 仅从操作者直接赋予的角色出发，沿包含边扩展。
	visited := map[string]bool{operator: true}
	current := []string{}
	for role := range r.memberships[operator] {
		if !visited[role] {
			visited[role] = true
			current = append(current, role)
		}
	}

	for dist := 1; len(current) > 0; dist++ {
		var (
			best  override
			found bool
			next  []string
		)
		for _, role := range current {
			if r.probe != nil {
				r.probe.RolesVisited++
				r.probe.OverridesChecked++
			}
			key.subject = role
			if ov, ok := r.overrides[key]; ok {
				if !found || ov.seq > best.seq {
					best, found = ov, true
				}
			}
			for parent := range r.parents[role] {
				if !visited[parent] {
					visited[parent] = true
					next = append(next, parent)
				}
			}
		}
		// 本层出现覆盖：采用最新者并立即停止，不展开下一层。
		if found {
			return best, true, dist
		}
		current = next
	}
	return empty, false, -1
}

// withProbe 临时挂载探针执行 fn，返回解析结果与探针统计。
func (r *Resolver) withProbe(operator, instanceID, attr string, fallback Visibility, fn func()) (VisBasis, Probe) {
	saved := r.probe
	p := &Probe{}
	r.probe = p
	defer func() { r.probe = saved }()
	basis := r.resolve(operator, instanceID, attr, fallback)
	if fn != nil {
		fn()
	}
	return basis, *p
}
