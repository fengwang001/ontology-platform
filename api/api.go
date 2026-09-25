// Package api 是属性级访问控制的唯一入口：写时强制 + 读时组装。
//
// 写入（创建/更新）先校验全部字段的写权限，任一字段无权限即整条拒绝
// （ErrWriteDenied，含字段名），且本次调用零物化——对象无任何属性被
// 部分写入。查询谓词一旦引用不可见列即整条拒绝（ErrPredicateDenied，
// 含列名），绝不做 NULL/假值代换。
package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/access"
	"ontology/acl"
	"ontology/project"
)

// 哨兵错误，可用 errors.Is 区分。
var (
	ErrWriteDenied     = errors.New("api: write denied")
	ErrPredicateDenied = errors.New("api: predicate references invisible property")
	ErrInvalidArgument = errors.New("api: invalid argument")
	ErrNotFound        = errors.New("api: object not found")
)

// DeniedFieldsError 携带无写权限的字段名列表。
type DeniedFieldsError struct{ Fields []string }

func (e *DeniedFieldsError) Error() string {
	return fmt.Sprintf("%v: fields [%s]", ErrWriteDenied, strings.Join(e.Fields, ", "))
}

func (e *DeniedFieldsError) Is(target error) bool { return target == ErrWriteDenied }

// PredicateColumnError 携带谓词引用的不可见列名。
type PredicateColumnError struct{ Column string }

func (e *PredicateColumnError) Error() string {
	return fmt.Sprintf("%v: column %q", ErrPredicateDenied, e.Column)
}

func (e *PredicateColumnError) Is(target error) bool { return target == ErrPredicateDenied }

// Service 是写入与查询的唯一入口，持有对象存储与求值器。
type Service struct {
	eval    *access.Evaluator
	objects map[string]project.Object

	materialized int // 非导出计数器：累计实际物化（写入）的属性数
}

// NewService 创建服务。
func NewService(eval *access.Evaluator) *Service {
	return &Service{eval: eval, objects: make(map[string]project.Object)}
}

// Materialized 返回累计物化属性数（用于证明失败写入零物化、成功写入全量物化）。
func (s *Service) Materialized() int { return s.materialized }

// Create 创建对象。任一字段无写权限则整条拒绝，零物化。
func (s *Service) Create(subj acl.Subject, id string, attrs map[string]any) error {
	if id == "" {
		return fmt.Errorf("%w: empty object id", ErrInvalidArgument)
	}
	if len(attrs) == 0 {
		return fmt.Errorf("%w: no attributes", ErrInvalidArgument)
	}
	if _, exists := s.objects[id]; exists {
		return fmt.Errorf("%w: object %q already exists", ErrInvalidArgument, id)
	}
	if err := s.checkWrite(subj, attrs); err != nil {
		return err
	}
	obj := make(project.Object, len(attrs))
	for k, v := range attrs {
		obj[k] = v
		s.materialized++
	}
	s.objects[id] = obj
	return nil
}

// Update 部分更新对象。任一字段无写权限则整条拒绝并指出字段名，零物化。
func (s *Service) Update(subj acl.Subject, id string, attrs map[string]any) error {
	if len(attrs) == 0 {
		return fmt.Errorf("%w: no attributes", ErrInvalidArgument)
	}
	obj, ok := s.objects[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if err := s.checkWrite(subj, attrs); err != nil {
		return err
	}
	for k, v := range attrs {
		obj[k] = v
		s.materialized++
	}
	return nil
}

// checkWrite 在物化之前校验全部字段的写权限。
func (s *Service) checkWrite(subj acl.Subject, attrs map[string]any) error {
	props := make([]string, 0, len(attrs))
	for k := range attrs {
		props = append(props, k)
	}
	if denied := s.eval.Denied(subj, props, acl.Write); len(denied) > 0 {
		return &DeniedFieldsError{Fields: denied}
	}
	return nil
}

// Query 按谓词查询。谓词引用不可见列时整条拒绝；否则对命中对象做读投影。
func (s *Service) Query(subj acl.Subject, pred Predicate) ([]project.Object, error) {
	for _, col := range pred.Columns() {
		if !s.eval.Allowed(subj, col, acl.Read) {
			return nil, &PredicateColumnError{Column: col}
		}
	}
	ids := make([]string, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	proj := project.NewProjector(s.eval, subj)
	var out []project.Object
	for _, id := range ids {
		if pred.match(s.objects[id]) {
			out = append(out, proj.Apply(s.objects[id]))
		}
	}
	return out, nil
}
