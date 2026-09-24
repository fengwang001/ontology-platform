// Package importer 编排多阶段导入：Prepare → Write → Verify → Commit。
package importer

import (
	"errors"
	"fmt"
	"time"

	"ontology/batch"
	"ontology/progress"
	"ontology/store"
)

// Source 按下标生成记录，使内存中同时驻留的记录数有界。
type Source func(i int) (key string, data []byte)

// Importer 把一个批次导入目标存储，支持断点续传与幂等重入。
type Importer struct {
	Store *store.Store
	Dir   string // 进度与锁文件目录
	N     int    // 进度刷新粒度（条）
	Limit int    // 内存中同时驻留记录数上限；<=0 不限制

	now func() time.Time
	ttl time.Duration

	rewritten int // 续传时重新写入（幂等重放）的记录数
	cur, peak int // 当前/历史峰值驻留记录数
}

// New 构造导入器；n 为进度刷新粒度。
func New(st *store.Store, dir string, n int) *Importer {
	if n < 1 {
		n = 1
	}
	return &Importer{Store: st, Dir: dir, N: n, now: time.Now, ttl: time.Hour}
}

// SetClock 注入时钟，用于锁过期判定。
func (im *Importer) SetClock(f func() time.Time) { im.now = f }

// Rewritten 返回续传中幂等重放的记录数。
func (im *Importer) Rewritten() int { return im.rewritten }

// Peak 返回内存驻留记录数的历史峰值。
func (im *Importer) Peak() int { return im.peak }

// Import 执行全部阶段。done=true 表示本批次此前已 Commit（重复导入），
// 不是错误；此时不执行任何写入。
func (im *Importer) Import(m batch.Manifest, src Source) (done bool, err error) {
	p, release, done, err := im.prepare(m)
	if err != nil || done {
		return done, err
	}
	defer release()
	if err := im.write(m, src, p); err != nil {
		return false, err
	}
	if err := im.verify(m); err != nil {
		return false, err
	}
	return false, im.commit(p)
}

// prepare：校验清单 → 占锁 → 加载进度并以存储为准截断有效区间。
func (im *Importer) prepare(m batch.Manifest) (*progress.Progress, func(), bool, error) {
	if err := m.Validate(); err != nil {
		return nil, nil, false, err
	}
	release, err := progress.Acquire(im.Dir, m.ID, im.now(), im.ttl)
	if err != nil {
		return nil, nil, false, err
	}
	p, lerr := progress.Load(im.Dir, m.ID)
	var le *progress.LoadError
	if lerr != nil && !errors.As(lerr, &le) {
		release()
		return nil, nil, false, lerr
	}
	if p.Committed {
		release()
		return p, nil, true, nil
	}
	p.Total = len(m.Keys)
	// 以存储为准：进度声称的区间逐键核对，遇第一个缺失键即截断。
	var ivls [][2]int
	for _, iv := range p.Ivls {
		end := iv[0]
		for end < iv[1] && end < len(m.Keys) && im.Store.Has(m.Keys[end]) {
			end++
		}
		if end > iv[0] {
			ivls = append(ivls, [2]int{iv[0], end})
		}
	}
	p.Ivls = ivls
	return p, release, false, nil
}

// write：跳过已确认区间，逐条幂等写入，每 N 条刷一次进度。
func (im *Importer) write(m batch.Manifest, src Source, p *progress.Progress) error {
	pending := 0
	for i := 0; i < len(m.Keys); i++ {
		if p.Covers(i) {
			continue
		}
		key, data := src(i)
		im.cur++
		if im.cur > im.peak {
			im.peak = im.cur
		}
		if im.Limit > 0 && im.cur > im.Limit {
			return fmt.Errorf("importer: resident limit %d exceeded", im.Limit)
		}
		existed, err := im.Store.Write(key, data)
		im.cur--
		if err != nil {
			return err
		}
		if existed {
			im.rewritten++
		}
		p.Add(i)
		pending++
		if pending >= im.N {
			if err := progress.Save(im.Dir, p); err != nil {
				return err
			}
			pending = 0
		}
	}
	return progress.Save(im.Dir, p)
}

// verify：线性确认清单全部键已落库。
func (im *Importer) verify(m batch.Manifest) error {
	for _, k := range m.Keys {
		if !im.Store.Has(k) {
			return fmt.Errorf("importer: verify: key %q missing", k)
		}
	}
	return nil
}

// commit：写入 committed=1 并落盘；锁由 Import 的 defer 释放。
func (im *Importer) commit(p *progress.Progress) error {
	p.Committed = true
	return progress.Save(im.Dir, p)
}
