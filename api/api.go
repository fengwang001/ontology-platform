// Package api 是对外门面：构造、四个 keyed 操作、宽表读取与自检。
package api

import (
	"fmt"

	"ontology/join"
	"ontology/jrow"
)

// Service 对外服务，并发安全。
type Service struct {
	j *join.J
}

// New 创建服务；maxKeys <= 0 返回 ErrBadMaxKeys。
func New(maxKeys int) (*Service, error) {
	j, err := join.New(maxKeys)
	if err != nil {
		return nil, err
	}
	return &Service{j: j}, nil
}

func (s *Service) PutL(key string, lv int) ([]jrow.Change, error) { return s.j.PutL(key, lv) }
func (s *Service) PutR(key string, rv int) ([]jrow.Change, error) { return s.j.PutR(key, rv) }
func (s *Service) DelL(key string) ([]jrow.Change, error)         { return s.j.DelL(key) }
func (s *Service) DelR(key string) ([]jrow.Change, error)         { return s.j.DelR(key) }

// WideTable 按 key 升序返回全部宽表行。
func (s *Service) WideTable() []jrow.WideRow { return s.j.WideTable() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	const N = 64
	s, err := New(N * 4)
	if err != nil {
		return err
	}
	var log []jrow.Change
	ml, mr := map[string]int{}, map[string]int{}
	// 确定性操作序列：覆盖新增、双侧匹配、值更新、删除。
	for i := 0; i < N; i++ {
		k := fmt.Sprintf("k%02d", (i*7)%24)
		var chs []jrow.Change
		switch i % 5 {
		case 0, 1:
			ml[k] = i
			chs, err = s.PutL(k, i)
		case 2, 3:
			mr[k] = i * 10
			chs, err = s.PutR(k, i*10)
		default:
			if _, ok := ml[k]; ok {
				delete(ml, k)
				chs, err = s.DelL(k)
			}
		}
		if err != nil {
			return fmt.Errorf("selfcheck 操作失败: %w", err)
		}
		log = append(log, chs...)
	}
	// 不变量 1：WideTable 与朴素 inner join 重算逐行一致。
	wide := s.WideTable()
	want := 0
	for k := range ml {
		if _, ok := mr[k]; ok {
			want++
		}
	}
	if len(wide) != want {
		return fmt.Errorf("selfcheck 不变量1: 宽表 %d 行, 朴素重算 %d 行", len(wide), want)
	}
	for _, w := range wide {
		if lv, ok := ml[w.Key]; !ok || lv != w.LV || mr[w.Key] != w.RV {
			return fmt.Errorf("selfcheck 不变量1: 行 %+v 与朴素重算不符", w)
		}
	}
	// 不变量 2：下游按前缀应用日志，- 必须恰好撤回该 key 当前行。
	down := map[string][2]int{}
	for _, c := range log {
		if c.Op == '+' {
			down[c.Key] = [2]int{c.LV, c.RV}
		} else if cur, ok := down[c.Key]; !ok || cur != [2]int{c.LV, c.RV} {
			return fmt.Errorf("selfcheck 不变量2: 撤回了不存在或不等的行 %+v", c)
		} else {
			delete(down, c.Key)
		}
	}
	// 不变量 3：重复更新幂等，无输出。
	if _, err := s.PutL("idem", 7); err != nil {
		return fmt.Errorf("selfcheck 不变量3: %w", err)
	}
	if chs, err := s.PutL("idem", 7); err != nil || chs != nil {
		return fmt.Errorf("selfcheck 不变量3: 重复更新产生了输出")
	}
	// 不变量 4：被拒操作不留痕。
	before := s.WideTable()
	if _, err := s.DelL("不存在"); err == nil {
		return fmt.Errorf("selfcheck 不变量4: 删除不存在 key 未报错")
	}
	if _, err := s.PutL("", 1); err == nil {
		return fmt.Errorf("selfcheck 不变量4: 空 key 未报错")
	}
	after := s.WideTable()
	if len(before) != len(after) {
		return fmt.Errorf("selfcheck 不变量4: 被拒操作改变了状态")
	}
	for i := range before {
		if before[i] != after[i] {
			return fmt.Errorf("selfcheck 不变量4: 被拒操作改变了状态")
		}
	}
	return nil
}
