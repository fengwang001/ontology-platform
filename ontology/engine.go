package ontology

import (
	"bytes"
	"sort"
)

// cloneFile 返回单个文件的深拷贝（复制键字节）。
func cloneFile(f File) File {
	cp := f
	if f.Smallest != nil {
		cp.Smallest = append([]byte(nil), f.Smallest...)
	}
	if f.Largest != nil {
		cp.Largest = append([]byte(nil), f.Largest...)
	}
	return cp
}

// cloneVersion 返回版本的深拷贝。
func cloneVersion(v Version) Version {
	cp := v
	for l := range v.Files {
		if v.Files[l] == nil {
			continue
		}
		files := make([]File, len(v.Files[l]))
		for i, f := range v.Files[l] {
			files[i] = cloneFile(f)
		}
		cp.Files[l] = files
	}
	return cp
}

// cloneEdit 返回编辑的深拷贝。
func cloneEdit(e Edit) Edit {
	cp := e
	if e.Adds != nil {
		cp.Adds = make([]File, len(e.Adds))
		for i, f := range e.Adds {
			cp.Adds[i] = cloneFile(f)
		}
	}
	if e.Dels != nil {
		cp.Dels = make([]File, len(e.Dels))
		for i, f := range e.Dels {
			cp.Dels[i] = cloneFile(f)
		}
	}
	cp.LogNumber = clonePtr(e.LogNumber)
	cp.NextFile = clonePtr(e.NextFile)
	cp.LastSeq = clonePtr(e.LastSeq)
	return cp
}

func clonePtr(p *uint64) *uint64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// cloneRecord 返回记录的深拷贝。
func cloneRecord(r Record) Record {
	cp := r
	if r.Snapshot != nil {
		v := cloneVersion(*r.Snapshot)
		cp.Snapshot = &v
	}
	if r.Edit != nil {
		e := cloneEdit(*r.Edit)
		cp.Edit = &e
	}
	return cp
}

// cloneDisk 返回磁盘内容的深拷贝。
func cloneDisk(d Disk) Disk {
	cp := Disk{Current: d.Current, Threshold: d.Threshold}
	if d.Manifests != nil {
		cp.Manifests = make(map[uint64][]Record, len(d.Manifests))
		for num, recs := range d.Manifests {
			out := make([]Record, len(recs))
			for i, r := range recs {
				out[i] = cloneRecord(r)
			}
			cp.Manifests[num] = out
		}
	}
	return cp
}

// sortLevel 使某一层的文件排列符合约定：
// 第 0 层按 Num 从大到小；第 1..6 层按 Smallest 升序。
func sortLevel(files []File, level int) {
	if level == 0 {
		sort.SliceStable(files, func(i, j int) bool { return files[i].Num > files[j].Num })
		return
	}
	sort.SliceStable(files, func(i, j int) bool {
		return bytes.Compare(files[i].Smallest, files[j].Smallest) < 0
	})
}

// checkFile 校验单个文件描述是否合法。
func checkFile(f File) bool {
	if f.Level < 0 || f.Level > 6 {
		return false
	}
	if f.Num == 0 || len(f.Smallest) == 0 || len(f.Largest) == 0 {
		return false
	}
	if bytes.Compare(f.Smallest, f.Largest) > 0 {
		return false
	}
	return true
}

// validEditResult 是 checkEdit 的产物：校验通过后的新版本，以及
// 第 1..6 层重叠检查实际使用的探针次数。
type validEditResult struct {
	version Version
	probes  int
}

// checkEdit 在基础版本 base 上校验并应用编辑 e。
//
// 严格按以下次序检查并返回第一个成立的错误：
//  1. ErrParam    非法文件描述、或空编辑（Adds/Dels 皆空且三个指针皆 nil）
//  2. ErrNoFile   按 Dels 顺序删除时找不到对应 (层, Num)
//  3. ErrDupFile  删除后，Add 的 Num 已存在于任一层、或与更早的 Add 同号
//  4. ErrRegress  给出的 NextFile/LogNumber/LastSeq 小于当前值
//  5. ErrLogAhead 有效 NextFile=max(当前值, 给出值, 所有 Add Num+1)，
//     给出的 LogNumber 必须小于该有效值
//  6. ErrOverlap  编辑后第 1..6 层存在闭区间重叠（首尾相接也算）
//
// fullChecks 为 false（Recover 回放）时跳过第 4、5 步：
// LogNumber/LastSeq 只在给出时取给出值，NextFile 仍按有效值抬高。
func checkEdit(base Version, e Edit, fullChecks bool) (validEditResult, error) {
	empty := len(e.Adds) == 0 && len(e.Dels) == 0 &&
		e.LogNumber == nil && e.NextFile == nil && e.LastSeq == nil
	if empty {
		return validEditResult{}, ErrParam
	}
	for _, f := range e.Adds {
		if !checkFile(f) {
			return validEditResult{}, ErrParam
		}
	}
	for _, f := range e.Dels {
		if f.Level < 0 || f.Level > 6 || f.Num == 0 {
			return validEditResult{}, ErrParam
		}
	}

	v := cloneVersion(base)

	// 第二步：按 Dels 顺序逐个删除。
	for _, del := range e.Dels {
		files := v.Files[del.Level]
		idx := -1
		for i, f := range files {
			if f.Num == del.Num {
				idx = i
				break
			}
		}
		if idx < 0 {
			return validEditResult{}, ErrNoFile
		}
		v.Files[del.Level] = append(files[:idx], files[idx+1:]...)
	}

	// 第三步：重复编号检查（任一层已存在 + 本编辑内更早 Add 同号）。
	seen := make(map[uint64]bool, len(e.Adds))
	for _, add := range e.Adds {
		if seen[add.Num] {
			return validEditResult{}, ErrDupFile
		}
		seen[add.Num] = true
		for _, files := range v.Files {
			for _, f := range files {
				if f.Num == add.Num {
					return validEditResult{}, ErrDupFile
				}
			}
		}
	}

	// 先确定有效 NextFile。
	effectiveNext := v.NextFile
	if e.NextFile != nil && *e.NextFile > effectiveNext {
		effectiveNext = *e.NextFile
	}
	for _, add := range e.Adds {
		if add.Num+1 > effectiveNext {
			effectiveNext = add.Num + 1
		}
	}

	if fullChecks {
		// 第四步：编号不得回退（恰等允许；未给出的字段不参与判定）。
		if e.NextFile != nil && *e.NextFile < base.NextFile {
			return validEditResult{}, ErrRegress
		}
		if e.LogNumber != nil && *e.LogNumber < base.LogNumber {
			return validEditResult{}, ErrRegress
		}
		if e.LastSeq != nil && *e.LastSeq < base.LastSeq {
			return validEditResult{}, ErrRegress
		}
		// 第五步：给出的 LogNumber 必须严格小于有效 NextFile。
		if e.LogNumber != nil && *e.LogNumber >= effectiveNext {
			return validEditResult{}, ErrLogAhead
		}
	}

	// 执行加入：克隆后插入，再按层约定排序。
	for _, add := range e.Adds {
		lvl := add.Level
		v.Files[lvl] = append(v.Files[lvl], cloneFile(add))
		sortLevel(v.Files[lvl], lvl)
	}

	// 第六步：仅对有新增的 1..6 层，与插入位置的前驱/后继比较。
	addedAt := make(map[int]bool)
	for _, add := range e.Adds {
		if add.Level > 0 {
			addedAt[add.Level] = true
		}
	}
	probes := 0
	for lvl := range addedAt {
		files := v.Files[lvl]
		for _, add := range e.Adds {
			if add.Level != lvl {
				continue
			}
			idx := -1
			for i, f := range files {
				if f.Num == add.Num {
					idx = i
					break
				}
			}
			if idx > 0 {
				probes++
				if bytes.Compare(files[idx-1].Largest, add.Smallest) >= 0 {
					return validEditResult{}, ErrOverlap
				}
			}
			if idx >= 0 && idx+1 < len(files) {
				probes++
				if bytes.Compare(add.Largest, files[idx+1].Smallest) >= 0 {
					return validEditResult{}, ErrOverlap
				}
			}
		}
	}

	v.NextFile = effectiveNext
	if e.LogNumber != nil {
		v.LogNumber = *e.LogNumber
	}
	if e.LastSeq != nil {
		v.LastSeq = *e.LastSeq
	}

	return validEditResult{version: v, probes: probes}, nil
}
