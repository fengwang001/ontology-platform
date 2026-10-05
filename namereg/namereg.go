// Package namereg 维护每服独立的名字登记与离开保留。
// 名字被本服某角色持有即被占用；保留在 now < expiry 时有效，取等即释放。
// 到期判定是惰性的：访问时比较 now 与到期时刻，不做全表清扫。
// 本包不加锁，并发安全由上层 transfer.System 的互斥锁保证。
package namereg

// reservation 是一条名字保留：owner 在 expiry（不含）之前可取回该名。
type reservation struct {
	owner  string
	expiry int64
}

// shardNames 是一个服的名字表。
type shardNames struct {
	holder map[string]string      // name -> 持有者 char
	byChar map[string]string      // char -> 持有的 name
	resv   map[string]reservation // name -> 保留
}

// NameReg 是全部服的名字登记处。probes 统计名字判定触达的记录数，
// 用于证明判定代价与服内角色数无关。
type NameReg struct {
	shards map[int64]*shardNames
	probes int
}

// New 返回空的名字登记处。
func New() *NameReg {
	return &NameReg{shards: make(map[int64]*shardNames)}
}

// AddShard 为一个服建立名字表。
func (n *NameReg) AddShard(sid int64) {
	n.shards[sid] = &shardNames{
		holder: make(map[string]string),
		byChar: make(map[string]string),
		resv:   make(map[string]reservation),
	}
}

// Probes 返回名字判定累计触达的记录数。
func (n *NameReg) Probes() int { return n.probes }

// ResetProbes 清零 probes 计数器。
func (n *NameReg) ResetProbes() { n.probes = 0 }

// Status 判定 name 在 sid 服对 self 而言的状态：
// occupied 表示被某角色持有；reserved 表示存在有效保留且保留者不是 self。
// 每次判定只触达常数条记录（占用 1 次、保留 1 次）。
func (n *NameReg) Status(sid int64, name, self string, now int64) (occupied, reserved bool) {
	s := n.shards[sid]
	n.probes++
	if _, ok := s.holder[name]; ok {
		return true, false
	}
	n.probes++
	res, ok := s.resv[name]
	if !ok {
		return false, false
	}
	if now >= res.expiry { // 取等即释放：惰性删除，O(1)，不清扫
		delete(s.resv, name)
		return false, false
	}
	return false, res.owner != self
}

// Hold 登记 char 持有 name，并清除本人在该名上的保留。
func (n *NameReg) Hold(sid int64, name, char string) {
	s := n.shards[sid]
	s.holder[name] = char
	s.byChar[char] = name
	if res, ok := s.resv[name]; ok && res.owner == char {
		delete(s.resv, name)
	}
}

// Release 解除 char 的持有（改名或离开），返回被释放的名字（无则 ""）。
func (n *NameReg) Release(sid int64, char string) string {
	s := n.shards[sid]
	name, ok := s.byChar[char]
	if !ok {
		return ""
	}
	delete(s.byChar, char)
	delete(s.holder, name)
	return name
}

// Reserve 为 owner 保留 name 至 expiry（不含）。
func (n *NameReg) Reserve(sid int64, name, owner string, expiry int64) {
	n.shards[sid].resv[name] = reservation{owner: owner, expiry: expiry}
}
