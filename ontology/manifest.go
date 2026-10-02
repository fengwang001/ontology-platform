package ontology

import "sync"

// Manager 是版本清单管理器。零值不可用，须通过 New 或 Open 得到。
type Manager struct {
	mu      sync.Mutex
	t       int
	ver     Version
	current uint64
	disk    Disk
	// probes 记录最近一次被接受的 Apply 在重叠检查中使用的探针次数。
	probes int
}

// New 创建阈值为 T 的管理器。初始版本为空（LogNumber=0、NextFile=2、
// LastSeq=0），磁盘上已有清单 1（一条 Snapshot），CURRENT 指向 1。
// T < 1 时返回 ErrParam。
func New(T int) (*Manager, error) {
	if T < 1 {
		return nil, ErrParam
	}
	ver := Version{NextFile: 2}
	d := Disk{
		Manifests: map[uint64][]Record{
			1: {{Snapshot: &ver}},
		},
		Current:   1,
		Threshold: T,
	}
	return &Manager{t: T, ver: ver, current: 1, disk: d}, nil
}

// Apply 校验并追加一条版本编辑；成功且清单记录数超过 T 时原子轮转。
// Apply 与其触发的 Rotate 对外是同一个原子步骤；被拒绝的编辑不改版本，
// 也不追加记录。
func (m *Manager) Apply(e Edit) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, err := checkEdit(m.ver, e, true)
	if err != nil {
		return err
	}

	// 整体生效：更新版本，编辑原样追加到当前清单。
	m.ver = res.version
	m.probes = res.probes
	rec := Record{Edit: ptrEdit(cloneEdit(e))}
	m.disk.Manifests[m.current] = append(m.disk.Manifests[m.current], rec)

	// 追加后记录数大于 T（恰等不轮转）时，连带 Rotate 一步完成。
	if len(m.disk.Manifests[m.current]) > m.t {
		m.rotateLocked()
	}
	return nil
}

// Rotate 消耗一个文件编号并写出新快照清单，翻转 CURRENT，返回新清单号。
func (m *Manager) Rotate() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rotateLocked()
}

// RotateUnflipped 与 Rotate 做相同的编号消耗与新清单写入（活动版本的
// NextFile 同样加 1），但不改 CURRENT，用来模拟换清单中途崩溃；之后的
// 编辑仍追加到旧清单。
func (m *Manager) RotateUnflipped() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	manifestNum := m.ver.NextFile
	m.ver.NextFile++
	snap := cloneVersion(m.ver)
	m.disk.Manifests[manifestNum] = []Record{{Snapshot: &snap}}
	return manifestNum
}

// Disk 返回磁盘内容（各清单号到记录列表、CURRENT）的深拷贝。
// 记录的 Torn 标记表示该记录只写了一半，可由调用方在返回值上构造。
func (m *Manager) Disk() Disk {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneDisk(m.disk)
}

// View 返回当前版本的深拷贝。第 0 层文件按 Num 从大到小排列；
// 第 1..6 层按 Smallest 升序排列。
func (m *Manager) View() Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneVersion(m.ver)
}

// overlapProbes 返回最近一次被接受的 Apply 在第 1..6 层重叠检查中
// 使用的探针次数（非导出，供包内测试断言 2*|Adds| 的上限）。
func (m *Manager) overlapProbes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.probes
}

// rotateLocked 是 Rotate 的不加锁内核：M=当前 NextFile，NextFile 加 1，
// 新建清单 M，其唯一记录为 Snapshot（含全部文件与 LogNumber、加 1 之后
// 的 NextFile、LastSeq），然后 CURRENT 指向 M 并返回 M。
func (m *Manager) rotateLocked() uint64 {
	manifestNum := m.ver.NextFile
	m.ver.NextFile++
	snap := cloneVersion(m.ver)
	m.disk.Manifests[manifestNum] = []Record{{Snapshot: &snap}}
	m.current = manifestNum
	m.disk.Current = manifestNum
	return manifestNum
}

func ptrEdit(e Edit) *Edit { return &e }

// Recover 从磁盘恢复版本。
//
// CURRENT 指向的清单不存在时返回 ErrNoCurrent。按序回放 CURRENT 清单：
//   - 首条必须是未撕裂的 Snapshot，否则返回带下标 0 的 ErrCorrupt；
//   - 之后再出现 Snapshot，返回带下标的 ErrCorrupt；
//   - 撕裂的 Edit：若它是最后一条（其后无记录）则忽略，否则 ErrCorrupt；
//   - 未撕裂的 Edit 按 Apply 的生效逻辑回放（NextFile 同样取有效值，
//     LogNumber/LastSeq 仅在给出时取给出值），但只做第一、二、三、六步
//     检查（不做 ErrRegress/ErrLogAhead）；任何检查失败都包装为
//     *CorruptError——errors.Is 同时命中 ErrCorrupt 与具体原因，并带记录下标。
//
// 恢复得到的 NextFile 取 max(回放所得, 磁盘上最大清单号+1)，使崩溃时
// 遗留的孤立清单编号不被复用。
func Recover(d Disk) (Version, error) {
	recs, ok := d.Manifests[d.Current]
	if !ok {
		return Version{}, ErrNoCurrent
	}
	if len(recs) == 0 || recs[0].Snapshot == nil || recs[0].Torn {
		return Version{}, &CorruptError{Index: 0, Err: ErrCorrupt}
	}

	ver := cloneVersion(*recs[0].Snapshot)
	for i := 1; i < len(recs); i++ {
		r := recs[i]
		if r.Edit == nil || r.Snapshot != nil {
			return Version{}, &CorruptError{Index: i, Err: ErrCorrupt}
		}
		if r.Torn {
			// 撕裂记录只允许出现在清单末尾。
			if i != len(recs)-1 {
				return Version{}, &CorruptError{Index: i, Err: ErrCorrupt}
			}
			break
		}
		res, err := checkEdit(ver, *r.Edit, false)
		if err != nil {
			return Version{}, &CorruptError{Index: i, Err: err}
		}
		ver = res.version
	}

	// 孤立清单编号不得复用。
	var maxManifest uint64
	for num := range d.Manifests {
		if num > maxManifest {
			maxManifest = num
		}
	}
	if maxManifest+1 > ver.NextFile {
		ver.NextFile = maxManifest + 1
	}
	return ver, nil
}

// Open 在 Recover 之后以恢复出的版本为活动版本，并立即 Rotate 一次得到
// 新清单；旧清单原样保留，磁盘使用输入 d 的深拷贝。
func Open(d Disk) (*Manager, error) {
	ver, err := Recover(d)
	if err != nil {
		return nil, err
	}
	t := d.Threshold
	if t < 1 {
		t = 1
	}
	disk := cloneDisk(d)
	disk.Threshold = t
	m := &Manager{
		t:       t,
		ver:     ver,
		current: d.Current,
		disk:    disk,
	}
	m.rotateLocked()
	return m, nil
}
