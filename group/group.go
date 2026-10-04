// Package group 维护设备、分组与分组成员关系。
package group

import "errors"
import "sort"

// 错误集合。
var (
	ErrInvalid       = errors.New("group: invalid argument")
	ErrNotFound      = errors.New("group: not found")
	ErrExists        = errors.New("group: already exists")
	ErrNotEmpty      = errors.New("group: group not empty")
	ErrTooManyGroups = errors.New("group: too many groups for device")
)

const (
	// Star 是内置分组名。
	Star         = "*"
	starPriority = -1
	// MaxPriority 是允许的最大分组优先级。
	MaxPriority = 1000
	// MaxMembership 是每台设备显式所属分组数上限的可取最大值。
	MaxMembership = 16
)

// ValidName 判定名字是否为 1..64 字节的非空字节串。
// "*" 为保留的内置分组名，任何普通对象名都不得使用。
func ValidName(name string) bool {
	if name == Star || len(name) < 1 || len(name) > 64 {
		return false
	}
	return true
}

// Group 是一个分组的静态信息。
type Group struct {
	Name     string
	Priority int
	members  map[string]struct{}
}

// State 是 group 包的全部状态。
type State struct {
	gmax    int
	groups  map[string]*Group
	devices map[string]map[string]struct{}
}

// New 创建状态并建立内置分组 "*"。
func New(gmax int) (*State, error) {
	if gmax < 1 || gmax > MaxMembership {
		return nil, ErrInvalid
	}
	s := &State{
		gmax:    gmax,
		groups:  map[string]*Group{},
		devices: map[string]map[string]struct{}{},
	}
	s.groups[Star] = &Group{Name: Star, Priority: starPriority}
	return s, nil
}

// Gmax 返回每台设备显式所属分组数上限。
func (s *State) Gmax() int { return s.gmax }

// Clone 返回深拷贝。
func (s *State) Clone() *State {
	c := &State{
		gmax:    s.gmax,
		groups:  make(map[string]*Group, len(s.groups)),
		devices: make(map[string]map[string]struct{}, len(s.devices)),
	}
	for name, g := range s.groups {
		gc := *g
		if g.members != nil {
			gc.members = make(map[string]struct{}, len(g.members))
			for dev := range g.members {
				gc.members[dev] = struct{}{}
			}
		}
		c.groups[name] = &gc
	}
	for dev, set := range s.devices {
		cs := make(map[string]struct{}, len(set))
		for g := range set {
			cs[g] = struct{}{}
		}
		c.devices[dev] = cs
	}
	return c
}

// HasDevice 报告设备是否存在。
func (s *State) HasDevice(dev string) bool {
	_, ok := s.devices[dev]
	return ok
}

// HasGroup 报告分组是否存在。
func (s *State) HasGroup(name string) bool {
	_, ok := s.groups[name]
	return ok
}

// GroupCount 返回显式分组数（不含内置 "*"）。
func (s *State) GroupCount() int { return len(s.groups) - 1 }

// DeviceCount 返回设备数。
func (s *State) DeviceCount() int { return len(s.devices) }

// Priority 返回分组优先级；分组不存在时 ok=false。
func (s *State) Priority(name string) (int, bool) {
	g, ok := s.groups[name]
	if !ok {
		return 0, false
	}
	return g.Priority, true
}

// SetPriority 修改非内置分组优先级。
func (s *State) SetPriority(name string, pr int) error {
	if name == Star || pr < 0 || pr > MaxPriority {
		return ErrInvalid
	}
	g, ok := s.groups[name]
	if !ok {
		return ErrNotFound
	}
	g.Priority = pr
	return nil
}

// AddDevice 增加设备，初始只隐式属于 "*"。
func (s *State) AddDevice(dev string) error {
	if !ValidName(dev) {
		return ErrInvalid
	}
	if _, ok := s.devices[dev]; ok {
		return ErrExists
	}
	s.devices[dev] = map[string]struct{}{}
	return nil
}

// RemoveDevice 删除设备。
func (s *State) RemoveDevice(dev string) error {
	if !ValidName(dev) {
		return ErrInvalid
	}
	if _, ok := s.devices[dev]; !ok {
		return ErrNotFound
	}
	for g := range s.devices[dev] {
		delete(s.groups[g].members, dev)
	}
	delete(s.devices, dev)
	return nil
}

// AddGroup 增加显式分组。
func (s *State) AddGroup(name string, pr int) error {
	if !ValidName(name) || pr < 0 || pr > MaxPriority {
		return ErrInvalid
	}
	if _, ok := s.groups[name]; ok {
		return ErrExists
	}
	s.groups[name] = &Group{Name: name, Priority: pr, members: map[string]struct{}{}}
	return nil
}

// RemoveGroup 删除仍无成员的非内置分组。
func (s *State) RemoveGroup(name string) error {
	if name == Star {
		return ErrInvalid
	}
	if !ValidName(name) {
		return ErrInvalid
	}
	g, ok := s.groups[name]
	if !ok {
		return ErrNotFound
	}
	if len(g.members) > 0 {
		return ErrNotEmpty
	}
	delete(s.groups, name)
	return nil
}

// AddMember 把设备加入分组。
func (s *State) AddMember(groupName, dev string) error {
	if groupName == Star || !ValidName(groupName) || !ValidName(dev) {
		return ErrInvalid
	}
	g, gok := s.groups[groupName]
	d, dok := s.devices[dev]
	if !gok || !dok {
		return ErrNotFound
	}
	if _, ok := g.members[dev]; ok {
		return ErrExists
	}
	if len(d) >= s.gmax {
		return ErrTooManyGroups
	}
	g.members[dev] = struct{}{}
	d[groupName] = struct{}{}
	return nil
}

// RemoveMember 把设备移出分组。
func (s *State) RemoveMember(groupName, dev string) error {
	if groupName == Star || !ValidName(groupName) || !ValidName(dev) {
		return ErrInvalid
	}
	g, gok := s.groups[groupName]
	d, dok := s.devices[dev]
	if !gok || !dok {
		return ErrNotFound
	}
	if _, ok := g.members[dev]; !ok {
		return ErrNotFound
	}
	delete(g.members, dev)
	delete(d, groupName)
	return nil
}

// MembershipCount 返回设备显式所属分组数。
func (s *State) MembershipCount(dev string) int { return len(s.devices[dev]) }

// MemberCount 返回分组的设备成员数。
func (s *State) MemberCount(groupName string) int { return len(s.groups[groupName].members) }

// GroupsOf 返回设备所属的全部分组名（显式分组加内置 "*"），结果按字节序排序。
func (s *State) GroupsOf(dev string) []string {
	d, ok := s.devices[dev]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(d)+1)
	for g := range d {
		out = append(out, g)
	}
	out = append(out, Star)
	sort.Strings(out)
	return out
}

// MembersOf 返回分组成员设备名，按字节序排序；分组不存在时返回 nil。
func (s *State) MembersOf(groupName string) []string {
	if groupName == Star {
		return s.AllDevices()
	}
	g, ok := s.groups[groupName]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(g.members))
	for dev := range g.members {
		out = append(out, dev)
	}
	sort.Strings(out)
	return out
}

// AllDevices 返回全部设备名，按字节序排序。
func (s *State) AllDevices() []string {
	out := make([]string, 0, len(s.devices))
	for dev := range s.devices {
		out = append(out, dev)
	}
	sort.Strings(out)
	return out
}

// AllGroups 返回全部分组名（含 "*"），按字节序排序。
func (s *State) AllGroups() []string {
	out := make([]string, 0, len(s.groups))
	for g := range s.groups {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}
