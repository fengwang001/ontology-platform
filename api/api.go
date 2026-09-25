// Package api 是属性级权限的唯一入口：写时强制、参数校验与查询组装。
// 写入含无权限字段即整条拒绝（ErrWriteDenied，含字段名），零静默丢弃；
// 谓词引用不可见列即整条拒绝（ErrPredicateDenied，含列名），不做代换。
// 见 DESIGN.md 推导点 2、3。
package api

import (
	"errors"
	"fmt"
	"sort"

	"ontology/access"
	"ontology/acl"
	"ontology/project"
)

// 哨兵错误，调用方用 errors.Is 区分。
var (
	ErrInvalidInput    = errors.New("api: invalid input")
	ErrWriteDenied     = errors.New("api: write denied")
	ErrPredicateDenied = errors.New("api: predicate references invisible property")
	ErrNotFound        = errors.New("api: object not found")
)

// Predicate 是一个查询谓词，可嵌套。
type Predicate struct {
	Op     string      // "gt" | "eq" | "isnull" | "not" | "and"
	Column string      // 叶子谓词引用的属性名
	Value  any         // 比较值
	Inner  *Predicate  // not 的操作数
	Args   []Predicate // and 的操作数
}

// Columns 返回谓词（含嵌套）引用的全部属性名，去重并排序。
func (p Predicate) Columns() []string {
	set := map[string]bool{}
	var walk func(p Predicate)
	walk = func(p Predicate) {
		if p.Column != "" {
			set[p.Column] = true
		}
		if p.Inner != nil {
			walk(*p.Inner)
		}
		for _, a := range p.Args {
			walk(a)
		}
	}
	walk(p)
	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols
}

// Service 是属性级权限 API 的唯一入口，对象存于内存。
type Service struct {
	store        *acl.Store
	ev           *access.Evaluator
	proj         *project.Projector
	objects      map[string]project.Object
	materialized int // 非导出计数器：实际物化（写入存储）的属性总数
}

// NewService 基于授权存储创建服务。
func NewService(store *acl.Store) *Service {
	ev := access.NewEvaluator(store)
	return &Service{
		store:   store,
		ev:      ev,
		proj:    project.NewProjector(ev),
		objects: make(map[string]project.Object),
	}
}

// checkWrite 校验主体对 attrs 全部字段的写权限；返回第一个被拒字段名。
func (s *Service) checkWrite(subject acl.Subject, attrs map[string]any) (string, bool) {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !s.ev.Allowed(subject, k, acl.Write) {
			return k, false
		}
	}
	return "", true
}

func validate(subject acl.Subject, id string, attrs map[string]any) error {
	if subject.ID == "" {
		return fmt.Errorf("%w: empty subject", ErrInvalidInput)
	}
	if id == "" || len(attrs) == 0 {
		return fmt.Errorf("%w: empty object id or attributes", ErrInvalidInput)
	}
	return nil
}

// Create 创建对象。含任一无写权限字段即整条拒绝，不写入任何属性。
func (s *Service) Create(subject acl.Subject, id string, attrs map[string]any) error {
	if err := validate(subject, id, attrs); err != nil {
		return err
	}
	if field, ok := s.checkWrite(subject, attrs); !ok {
		return fmt.Errorf("%w: property %q", ErrWriteDenied, field)
	}
	obj := make(project.Object, len(attrs))
	for k, v := range attrs {
		obj[k] = v
	}
	s.objects[id] = obj
	s.materialized += len(attrs)
	return nil
}

// Update 更新对象的部分字段。含任一无写权限字段即整条拒绝并指出字段名，
// 对象保持原样（零静默丢弃）。
func (s *Service) Update(subject acl.Subject, id string, attrs map[string]any) error {
	if err := validate(subject, id, attrs); err != nil {
		return err
	}
	obj, ok := s.objects[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if field, ok := s.checkWrite(subject, attrs); !ok {
		return fmt.Errorf("%w: property %q", ErrWriteDenied, field)
	}
	for k, v := range attrs {
		obj[k] = v
	}
	s.materialized += len(attrs)
	return nil
}

// Query 按谓词查询并返回投影后的读取视图。
// 谓词引用不可见列时整条拒绝（ErrPredicateDenied，含列名）。
func (s *Service) Query(subject acl.Subject, pred Predicate) ([]project.Object, error) {
	if subject.ID == "" {
		return nil, fmt.Errorf("%w: empty subject", ErrInvalidInput)
	}
	for _, col := range pred.Columns() {
		if !s.ev.Allowed(subject, col, acl.Read) {
			return nil, fmt.Errorf("%w: property %q", ErrPredicateDenied, col)
		}
	}
	var out []project.Object
	for _, obj := range s.objects {
		if eval(pred, obj) {
			out = append(out, s.proj.Project(obj, subject))
		}
	}
	return out, nil
}

// eval 对（已确认全部可见的）对象求值谓词。
func eval(p Predicate, obj project.Object) bool {
	switch p.Op {
	case "gt":
		return greater(obj[p.Column], p.Value)
	case "eq":
		return obj[p.Column] == p.Value
	case "isnull":
		_, exists := obj[p.Column]
		return !exists || obj[p.Column] == nil
	case "not":
		return !eval(*p.Inner, obj)
	case "and":
		for _, a := range p.Args {
			if !eval(a, obj) {
				return false
			}
		}
		return true
	}
	return false
}

func greater(a, b any) bool {
	if af, ok := a.(float64); ok {
		bf, ok2 := b.(float64)
		return ok2 && af > bf
	}
	as, ok := a.(string)
	bs, ok2 := b.(string)
	return ok && ok2 && as > bs
}

// Get 返回投影后的单个对象读取视图。
func (s *Service) Get(subject acl.Subject, id string) (project.Object, error) {
	obj, ok := s.objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return s.proj.Project(obj, subject), nil
}
