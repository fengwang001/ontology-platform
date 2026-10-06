package ontology

import "sort"

// EffectiveAt 返回 at 时刻的有效分数、来源与是否复核中；不受此后版本影响。
// 查询是只读操作：不推进时钟、不触发惰性失效、不写审计。
//
// 复杂度（可验证）：
//   - 记录定位为 map 查找 O(1)；
//   - 版本/申请/提案均只遍历该记录自身的切片（长度为该记录的版本数/申请数）；
//   - 不触碰其他学生或课程的任何记录，故开销不随记录总数增长。
func (e *Engine) EffectiveAt(studentID, courseID, termID string, at int64) (EffectiveView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(studentID, courseID, termID) {
		return EffectiveView{}, errf(ErrInvalid, "empty id in effective query")
	}
	rec := e.getRecord(studentID, courseID, termID)
	if rec == nil {
		return EffectiveView{}, errf(ErrNotFound, "no grade record for %s/%s/%s", studentID, courseID, termID)
	}

	view := EffectiveView{}
	found := false
	for i := len(rec.Versions) - 1; i >= 0; i-- {
		v := rec.Versions[i]
		if v.Effective <= at {
			view.Score, view.Source, found = v.Score, v.Source, true
			break
		}
	}
	if !found {
		return EffectiveView{}, errf(ErrNotFound, "no effective version at t=%d for %s", at, describeRecord(rec))
	}

	view.InReview = e.inReviewAt(rec, at)
	return view, nil
}

// inReviewAt 判断 at 时刻记录是否处于复核中。
// 复核中区间为 [申请打开, 结案)：申请打开当刻即为 true，
// 结案/通过/驳回/锁定清理当刻起为 false。
// 待审批提案期间申请尚未结案，仍属复核中；提案超时在其 deadline 之后
// 的下一次变更操作时才落地，因此在落地前的时点视图上仍视为复核中。
func (e *Engine) inReviewAt(rec *Record, at int64) bool {
	for _, r := range rec.Reviews {
		if r.OpenedAt <= at && (r.Status == ReviewOpen || at < r.ClosedAt) {
			return true
		}
	}
	return false
}

// TermAverageAt 返回学生某学期在 at 时刻的平均分（按当时有效分数）。
// 只计入在 at 时刻已有生效版本的课程；返回 (平均分, 计入课程数)。
// 经 studentTerm 反查索引，仅遍历该学生该学期的记录。
func (e *Engine) TermAverageAt(studentID, termID string, at int64) (float64, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(studentID, termID) {
		return 0, 0, errf(ErrInvalid, "empty id in average query")
	}
	recs := e.studentTerm[studentTermKey(termID, studentID)]
	if len(recs) == 0 {
		return 0, 0, errf(ErrNotFound, "no records for student %s in term %s", studentID, termID)
	}

	ordered := append([]*Record(nil), recs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].CourseID < ordered[j].CourseID })

	sum, n := 0, 0
	for _, rec := range ordered {
		for i := len(rec.Versions) - 1; i >= 0; i-- {
			if v := rec.Versions[i]; v.Effective <= at {
				sum += v.Score
				n++
				break
			}
		}
	}
	if n == 0 {
		return 0, 0, nil
	}
	return float64(sum) / float64(n), n, nil
}

// RecordVersions 返回版本链的只读拷贝（验证/测试用）。
func (e *Engine) RecordVersions(studentID, courseID, termID string) ([]Version, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !nonEmpty(studentID, courseID, termID) {
		return nil, errf(ErrInvalid, "empty id in versions query")
	}
	rec := e.getRecord(studentID, courseID, termID)
	if rec == nil {
		return nil, errf(ErrNotFound, "no grade record for %s/%s/%s", studentID, courseID, termID)
	}
	out := make([]Version, len(rec.Versions))
	copy(out, rec.Versions)
	return out, nil
}
