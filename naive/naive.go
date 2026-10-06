// Package naive 是多成员镜像块卷的独立朴素模型：逐块数组加线性扫描，
// 不追求任何效率，只追求与规约逐条对应，用于与正式实现做随机对照。
// 错误类别编号与 mirror.ErrKind 的声明顺序一致，便于逐位比较。
package naive

import "fmt"

// Kind 错误类别，编号与 mirror.ErrKind 一一对应。
type Kind int

const (
	KInvalidArgument Kind = iota
	KNoSuchMember
	KInvalidState
	KGenerationAhead
	KNotAuthoritative
	KVolumeUnavailable
)

// Err 是模型返回的错误类型。
type Err struct {
	Kind Kind
	Msg  string
}

func (e *Err) Error() string { return fmt.Sprintf("kind=%d: %s", e.Kind, e.Msg) }

func errf(k Kind, format string, args ...any) *Err {
	return &Err{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

// 成员状态。
const (
	Online = iota
	Faulted
	Resyncing
)

// Vol 是朴素模型卷。pending 用逐块布尔数组表示待同步块集合。
type Vol struct {
	numBlocks int
	limit     int
	gen       uint64
	state     []int
	faultGen  []uint64
	full      []bool
	pending   [][]bool
	data      [][]string
}

// New 创建模型卷，语义同 mirror.NewVolume。
func New(members, numBlocks, dirtyLimit int) (*Vol, error) {
	if members < 2 || members > 4 {
		return nil, errf(KInvalidArgument, "成员数须在 2 到 4 之间")
	}
	if numBlocks < 1 {
		return nil, errf(KInvalidArgument, "块数须为正")
	}
	if dirtyLimit < 0 {
		return nil, errf(KInvalidArgument, "脏区上限须非负")
	}
	v := &Vol{numBlocks: numBlocks, limit: dirtyLimit, gen: 1}
	for i := 0; i < members; i++ {
		v.state = append(v.state, Online)
		v.faultGen = append(v.faultGen, 0)
		v.full = append(v.full, false)
		v.pending = append(v.pending, nil)
		v.data = append(v.data, make([]string, numBlocks))
	}
	return v, nil
}

func (v *Vol) fault(i int) {
	if v.pending[i] == nil {
		v.pending[i] = make([]bool, v.numBlocks)
	}
	v.faultGen[i] = v.gen
	v.state[i] = Faulted
}

func (v *Vol) addDirty(i, block int) {
	if v.full[i] {
		return
	}
	v.pending[i][block] = true
	count := 0
	for b := 0; b < v.numBlocks; b++ {
		if v.pending[i][b] {
			count++
		}
	}
	if count > v.limit {
		v.pending[i] = make([]bool, v.numBlocks)
		v.full[i] = true
	}
}

// Write 语义同 mirror.Volume.Write。
func (v *Vol) Write(block int, value string, failed []int) error {
	if block < 0 || block >= v.numBlocks {
		return errf(KInvalidArgument, "块号越界")
	}
	failedSet := map[int]bool{}
	for _, id := range failed {
		if id < 0 || id >= len(v.state) {
			return errf(KNoSuchMember, "成员 %d 不存在", id)
		}
		failedSet[id] = true
	}
	anyOnline := false
	allFail := true
	for i := range v.state {
		if v.state[i] != Online {
			continue
		}
		anyOnline = true
		if !failedSet[i] {
			allFail = false
		}
	}
	if !anyOnline || allFail {
		return errf(KVolumeUnavailable, "卷不可用")
	}
	anyFault := false
	for i := range v.state {
		if v.state[i] != Online && v.state[i] != Resyncing {
			continue
		}
		if failedSet[i] {
			v.fault(i)
			anyFault = true
			continue
		}
		v.data[i][block] = value
	}
	if anyFault {
		v.gen++
	}
	for i := range v.state {
		switch v.state[i] {
		case Faulted:
			v.addDirty(i, block)
		case Resyncing:
			v.pending[i][block] = false
		}
	}
	return nil
}

// Read 语义同 mirror.Volume.Read。
func (v *Vol) Read(block int) (string, int, error) {
	if block < 0 || block >= v.numBlocks {
		return "", -1, errf(KInvalidArgument, "块号越界")
	}
	for i := range v.state {
		if v.state[i] == Online {
			return v.data[i][block], i, nil
		}
	}
	return "", -1, errf(KVolumeUnavailable, "卷不可用")
}

// ReportFault 语义同 mirror.Volume.ReportFault。
func (v *Vol) ReportFault(id int) error {
	if id < 0 || id >= len(v.state) {
		return errf(KNoSuchMember, "成员 %d 不存在", id)
	}
	if v.state[id] != Online && v.state[id] != Resyncing {
		return errf(KInvalidState, "状态不符")
	}
	v.fault(id)
	hasOnline := false
	for i := range v.state {
		if v.state[i] == Online {
			hasOnline = true
		}
	}
	if !hasOnline {
		for i := range v.state {
			if v.state[i] == Resyncing {
				v.fault(i)
			}
		}
	}
	v.gen++
	return nil
}

// Rejoin 语义同 mirror.Volume.Rejoin。
func (v *Vol) Rejoin(id int, diskGen uint64) error {
	if id < 0 || id >= len(v.state) {
		return errf(KNoSuchMember, "成员 %d 不存在", id)
	}
	if v.state[id] != Faulted {
		return errf(KInvalidState, "状态不符")
	}
	if diskGen > v.gen {
		return errf(KGenerationAhead, "世代超前")
	}
	allFaulted := true
	for i := range v.state {
		if v.state[i] != Faulted {
			allFaulted = false
		}
	}
	if allFaulted {
		best := 0
		for i := 1; i < len(v.state); i++ {
			if v.faultGen[i] > v.faultGen[best] {
				best = i
			}
		}
		if id != best {
			return errf(KNotAuthoritative, "非权威成员")
		}
		v.state[id] = Online
		v.pending[id] = nil
		v.full[id] = false
		for i := range v.state {
			if i == id {
				continue
			}
			v.pending[i] = make([]bool, v.numBlocks)
			v.full[i] = true
		}
		return nil
	}
	if diskGen == v.faultGen[id] && !v.full[id] {
		// 部分重同步：pending 保持现状。
	} else {
		v.pending[id] = make([]bool, v.numBlocks)
		for b := range v.pending[id] {
			v.pending[id][b] = true
		}
		v.full[id] = true
	}
	v.state[id] = Resyncing
	return nil
}

// ResyncAdvance 语义同 mirror.Volume.ResyncAdvance。
func (v *Vol) ResyncAdvance(id int, maxBlocks int) ([]int, error) {
	if maxBlocks < 1 {
		return nil, errf(KInvalidArgument, "推进上限须为正")
	}
	if id < 0 || id >= len(v.state) {
		return nil, errf(KNoSuchMember, "成员 %d 不存在", id)
	}
	if v.state[id] != Resyncing {
		return nil, errf(KInvalidState, "状态不符")
	}
	src := -1
	for i := range v.state {
		if v.state[i] == Online {
			src = i
			break
		}
	}
	if src < 0 {
		return nil, errf(KVolumeUnavailable, "卷不可用")
	}
	var copied []int
	remain := 0
	for b := 0; b < v.numBlocks; b++ {
		if !v.pending[id][b] {
			continue
		}
		if len(copied) < maxBlocks {
			v.data[id][b] = v.data[src][b]
			v.pending[id][b] = false
			copied = append(copied, b)
		} else {
			remain++
		}
	}
	if remain == 0 {
		v.state[id] = Online
		v.pending[id] = nil
		v.full[id] = false
	}
	return copied, nil
}

// Digest 输出完整状态的规范字符串：世代、每个成员的状态、故障世代、
// 全量标记、待同步块集合与全部盘面数据，用于逐位对照。
func (v *Vol) Digest() string {
	s := fmt.Sprintf("gen=%d", v.gen)
	for i := range v.state {
		s += fmt.Sprintf("|m%d st=%d fg=%d full=%v pend=", i, v.state[i], v.faultGen[i], v.full[i])
		if v.pending[i] == nil {
			s += "-"
		} else {
			s += "["
			for b := 0; b < v.numBlocks; b++ {
				if v.pending[i][b] {
					s += fmt.Sprintf("%d,", b)
				}
			}
			s += "]"
		}
		s += fmt.Sprintf(" data=%q", v.data[i])
	}
	return s
}
