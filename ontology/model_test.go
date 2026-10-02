package ontology

import (
	"bytes"
	"sort"
)

// naiveFile 是朴素模型里的文件。
type naiveFile struct {
	num               uint64
	smallest, largest []byte
}

// naiveRec 是朴素模型里的清单记录。
type naiveRec struct {
	snap *Version
	edit *Edit
	torn bool
}

// naiveModel 按题目规则独立实现的朴素参考模型：用 map 保存文件，
// 逐字段、逐步骤模拟管理器全部行为。
type naiveModel struct {
	t       int
	files   map[[2]uint64]naiveFile // (level, num) -> file
	logNo   uint64
	next    uint64
	seq     uint64
	mans    map[uint64][]naiveRec
	current uint64
}

func newNaiveModel(t int) *naiveModel {
	v := Version{NextFile: 2}
	return &naiveModel{
		t:       t,
		files:   map[[2]uint64]naiveFile{},
		next:    2,
		mans:    map[uint64][]naiveRec{1: {{snap: &v}}},
		current: 1,
	}
}

func (m *naiveModel) snapshotFiles() [7][]File {
	var byLevel [7][]File
	for key, f := range m.files {
		level := int(key[0])
		byLevel[level] = append(byLevel[level], File{
			Level:    level,
			Num:      f.num,
			Smallest: append([]byte(nil), f.smallest...),
			Largest:  append([]byte(nil), f.largest...),
		})
	}
	for level := 0; level < 7; level++ {
		sortLevel(byLevel[level], level)
	}
	return byLevel
}

func (m *naiveModel) view() Version {
	return Version{
		Files:     m.snapshotFiles(),
		LogNumber: m.logNo,
		NextFile:  m.next,
		LastSeq:   m.seq,
	}
}

func (m *naiveModel) disk() Disk {
	d := Disk{Manifests: map[uint64][]Record{}, Current: m.current, Threshold: m.t}
	for num, recs := range m.mans {
		for _, r := range recs {
			rec := Record{Torn: r.torn}
			if r.snap != nil {
				v := cloneVersion(*r.snap)
				rec.Snapshot = &v
			}
			if r.edit != nil {
				e := cloneEdit(*r.edit)
				rec.Edit = &e
			}
			d.Manifests[num] = append(d.Manifests[num], rec)
		}
	}
	return d
}

// markTorn 把清单 num 的第 idx 条记录改成只写了一半（Edit）。
func (m *naiveModel) markTorn(num uint64, idx int) {
	recs := m.mans[num]
	recs[idx].torn = true
	m.mans[num] = recs
}

func naiveValidFile(f File) bool {
	return f.Level >= 0 && f.Level <= 6 && f.Num != 0 &&
		len(f.Smallest) > 0 && len(f.Largest) > 0 &&
		bytes.Compare(f.Smallest, f.Largest) <= 0
}

func (m *naiveModel) cloneFiles() map[[2]uint64]naiveFile {
	cp := make(map[[2]uint64]naiveFile, len(m.files))
	for k, f := range m.files {
		cp[k] = naiveFile{
			num:      f.num,
			smallest: append([]byte(nil), f.smallest...),
			largest:  append([]byte(nil), f.largest...),
		}
	}
	return cp
}

// overlapAdded 检查：给定文件集合在 1..6 层上，新增文件与其前驱/后继
// 的闭区间是否重叠（首尾相接也算重叠）。
func overlapIn(files map[[2]uint64]naiveFile, adds []File) bool {
	added := make(map[[2]uint64]bool, len(adds))
	var byLevel [7][]naiveFile
	for key, f := range files {
		byLevel[key[0]] = append(byLevel[key[0]], f)
	}
	for _, add := range adds {
		if add.Level > 0 {
			added[[2]uint64{uint64(add.Level), add.Num}] = true
		}
	}
	for level := 1; level < 7; level++ {
		lf := byLevel[level]
		sort.SliceStable(lf, func(i, j int) bool {
			return bytes.Compare(lf[i].smallest, lf[j].smallest) < 0
		})
		for i, f := range lf {
			if !added[[2]uint64{uint64(level), f.num}] {
				continue
			}
			if i > 0 && bytes.Compare(lf[i-1].largest, f.smallest) >= 0 {
				return true
			}
			if i+1 < len(lf) && bytes.Compare(f.largest, lf[i+1].smallest) >= 0 {
				return true
			}
		}
	}
	return false
}

// validate 按规定次序检查（full=false 时跳过四、五步），
// 返回校验后的临时文件集合与有效 NextFile；不改动 m。
func (m *naiveModel) validate(e Edit, full bool) (map[[2]uint64]naiveFile, uint64, error) {
	empty := len(e.Adds) == 0 && len(e.Dels) == 0 &&
		e.LogNumber == nil && e.NextFile == nil && e.LastSeq == nil
	if empty {
		return nil, 0, ErrParam
	}
	for _, f := range e.Adds {
		if !naiveValidFile(f) {
			return nil, 0, ErrParam
		}
	}
	for _, f := range e.Dels {
		if f.Level < 0 || f.Level > 6 || f.Num == 0 {
			return nil, 0, ErrParam
		}
	}

	tmp := m.cloneFiles()
	for _, del := range e.Dels {
		key := [2]uint64{uint64(del.Level), del.Num}
		if _, ok := tmp[key]; !ok {
			return nil, 0, ErrNoFile
		}
		delete(tmp, key)
	}

	seen := map[uint64]bool{}
	for _, add := range e.Adds {
		if seen[add.Num] {
			return nil, 0, ErrDupFile
		}
		seen[add.Num] = true
		for level := 0; level < 7; level++ {
			if _, ok := tmp[[2]uint64{uint64(level), add.Num}]; ok {
				return nil, 0, ErrDupFile
			}
		}
	}

	eff := m.next
	if e.NextFile != nil && *e.NextFile > eff {
		eff = *e.NextFile
	}
	for _, add := range e.Adds {
		if add.Num+1 > eff {
			eff = add.Num + 1
		}
	}

	if full {
		if e.NextFile != nil && *e.NextFile < m.next {
			return nil, 0, ErrRegress
		}
		if e.LogNumber != nil && *e.LogNumber < m.logNo {
			return nil, 0, ErrRegress
		}
		if e.LastSeq != nil && *e.LastSeq < m.seq {
			return nil, 0, ErrRegress
		}
		if e.LogNumber != nil && *e.LogNumber >= eff {
			return nil, 0, ErrLogAhead
		}
	}

	for _, add := range e.Adds {
		tmp[[2]uint64{uint64(add.Level), add.Num}] = naiveFile{
			num:      add.Num,
			smallest: append([]byte(nil), add.Smallest...),
			largest:  append([]byte(nil), add.Largest...),
		}
	}
	if overlapIn(tmp, e.Adds) {
		return nil, 0, ErrOverlap
	}
	return tmp, eff, nil
}

func (m *naiveModel) apply(e Edit) error {
	tmp, eff, err := m.validate(e, true)
	if err != nil {
		return err
	}
	m.files = tmp
	m.next = eff
	if e.LogNumber != nil {
		m.logNo = *e.LogNumber
	}
	if e.LastSeq != nil {
		m.seq = *e.LastSeq
	}
	m.mans[m.current] = append(m.mans[m.current], naiveRec{edit: ptrEdit(cloneEdit(e))})
	if len(m.mans[m.current]) > m.t {
		m.rotate()
	}
	return nil
}

// replayEdit 是恢复路径上的回放：不做四、五步，不追加记录。
func (m *naiveModel) replayEdit(e Edit) error {
	tmp, eff, err := m.validate(e, false)
	if err != nil {
		return err
	}
	m.files = tmp
	m.next = eff
	if e.LogNumber != nil {
		m.logNo = *e.LogNumber
	}
	if e.LastSeq != nil {
		m.seq = *e.LastSeq
	}
	return nil
}

func (m *naiveModel) rotate() uint64 {
	num := m.next
	m.next++
	snap := m.view()
	snap.NextFile = m.next
	m.mans[num] = []naiveRec{{snap: &snap}}
	m.current = num
	return num
}

func (m *naiveModel) rotateUnflipped() uint64 {
	num := m.next
	m.next++
	snap := m.view()
	snap.NextFile = m.next
	m.mans[num] = []naiveRec{{snap: &snap}}
	return num
}

// loadSnapshot 用快照覆盖模型文件与三个指针。
func (m *naiveModel) loadSnapshot(v Version) {
	m.files = map[[2]uint64]naiveFile{}
	for level := range v.Files {
		for _, f := range v.Files[level] {
			m.files[[2]uint64{uint64(level), f.Num}] = naiveFile{
				num:      f.Num,
				smallest: append([]byte(nil), f.Smallest...),
				largest:  append([]byte(nil), f.Largest...),
			}
		}
	}
	m.logNo, m.next, m.seq = v.LogNumber, v.NextFile, v.LastSeq
}

func naiveRecover(d Disk) (Version, error) {
	recs, ok := d.Manifests[d.Current]
	if !ok {
		return Version{}, ErrNoCurrent
	}
	if len(recs) == 0 || recs[0].Snapshot == nil || recs[0].Torn {
		return Version{}, &CorruptError{Index: 0, Err: ErrCorrupt}
	}
	m := &naiveModel{t: d.Threshold, files: map[[2]uint64]naiveFile{},
		mans: map[uint64][]naiveRec{}}
	m.loadSnapshot(cloneVersion(*recs[0].Snapshot))
	for i := 1; i < len(recs); i++ {
		r := recs[i]
		if r.Edit == nil || r.Snapshot != nil {
			return Version{}, &CorruptError{Index: i, Err: ErrCorrupt}
		}
		if r.Torn {
			if i != len(recs)-1 {
				return Version{}, &CorruptError{Index: i, Err: ErrCorrupt}
			}
			break
		}
		if err := m.replayEdit(*r.Edit); err != nil {
			return Version{}, &CorruptError{Index: i, Err: err}
		}
	}
	var maxMan uint64
	for num := range d.Manifests {
		if num > maxMan {
			maxMan = num
		}
	}
	if maxMan+1 > m.next {
		m.next = maxMan + 1
	}
	return m.view(), nil
}

func allModelFiles(m *naiveModel) []File {
	out := m.snapshotFiles()
	var flat []File
	for level := range out {
		flat = append(flat, out[level]...)
	}
	return flat
}
