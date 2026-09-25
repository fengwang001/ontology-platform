// Package api 是属性级权限的唯一写入口与查询组装层。
//
// 写时强制（fail-closed，零静默丢弃）：Create / Update 先校验全部提交字段
// 的写权限，任一字段无权限即整条拒绝，错误指出字段名，且不物化任何字段。
// 谓词防泄露：Query 先校验谓词引用的每一列对调用者可读，
// 任一不可见即整条拒绝并指出列名，绝不做 NULL/假值代换。
// 读返回值经 project 投影；部分可见视图允许缺 schema 必填列。
package api

import (
	"errors"
	"fmt"
	"sort"

	"ontology/access"
	"ontology/acl"
	"ontology/project"
)

var (
	// ErrWriteDenied：写入含无写权限字段，错误文本指出字段名。
	ErrWriteDenied = errors.New("api: write denied on attribute")
	// ErrPredicateDenied：谓词引用无读权限列，错误文本指出列名。
	ErrPredicateDenied = errors.New("api: predicate references invisible attribute")
	// ErrInvalidArgument：参数校验失败（空对象、缺必填列等）。
	ErrInvalidArgument = errors.New("api: invalid argument")
	// ErrNotFound：更新/读取的对象不存在。
	ErrNotFound = errors.New("api: object not found")
)

// Schema 描述写入时的必填列（仅约束存储，不约束读视图）。
type Schema struct {
	Required []string
}

// Service 是唯一入口。用 NewService 构造。
type Service struct {
	store  *acl.Store
	schema Schema

	objects      map[string]project.Object
	orderedIDs   []string
	nextID       int
	materialized int // 非导出计数器：成功物化（整对象）次数
}

// NewService 创建服务。
func NewService(store *acl.Store, schema Schema) *Service {
	return &Service{store: store, schema: schema, objects: map[string]project.Object{}}
}

// Create 创建整对象：参数校验 → 全字段写权限校验 → 全量物化。
func (s *Service) Create(caller acl.Subject, obj map[string]any) (string, error) {
	if len(obj) == 0 {
		return "", fmt.Errorf("%w: empty object", ErrInvalidArgument)
	}
	for _, r := range s.schema.Required {
		if _, ok := obj[r]; !ok {
			return "", fmt.Errorf("%w: missing required attribute %q", ErrInvalidArgument, r)
		}
	}
	if err := s.checkWrite(caller, obj); err != nil {
		return "", err
	}
	s.nextID++
	id := fmt.Sprintf("obj-%d", s.nextID)
	stored := make(project.Object, len(obj))
	for k, v := range obj {
		stored[k] = v
	}
	s.objects[id] = stored
	s.orderedIDs = append(s.orderedIDs, id)
	s.materialized++
	return id, nil
}

// Update 对已有对象打部分字段补丁：存在性 → 全补丁字段写权限校验 → 整体应用。
// 任一字段无权限即拒绝，对象不发生任何变更（无部分写入）。
func (s *Service) Update(caller acl.Subject, id string, patch map[string]any) error {
	if len(patch) == 0 {
		return fmt.Errorf("%w: empty patch", ErrInvalidArgument)
	}
	if _, ok := s.objects[id]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err := s.checkWrite(caller, patch); err != nil {
		return err
	}
	for k, v := range patch {
		s.objects[id][k] = v
	}
	s.materialized++
	return nil
}

// Get 返回对象对 caller 的读投影；对象不存在返回 ErrNotFound。
func (s *Service) Get(caller acl.Subject, id string) (project.Object, error) {
	obj, ok := s.objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	pj := project.NewProjector()
	chk := access.NewChecker(s.store, caller)
	return pj.Project(obj, func(attr string) bool {
		return chk.Allowed(attr, access.Read)
	}), nil
}

// Query 执行查询：predicateCols 是谓词引用的全部列。
// 任一谓词列对 caller 不可读即整条拒绝（指出列名），否则返回全部对象的读投影。
func (s *Service) Query(caller acl.Subject, predicateCols []string) ([]project.Object, error) {
	chk := access.NewChecker(s.store, caller)
	for _, col := range predicateCols {
		if !chk.Allowed(col, access.Read) {
			return nil, fmt.Errorf("%w: %q", ErrPredicateDenied, col)
		}
	}
	pj := project.NewProjector()
	out := make([]project.Object, 0, len(s.orderedIDs))
	for _, id := range s.orderedIDs {
		out = append(out, pj.Project(s.objects[id], func(attr string) bool {
			return chk.Allowed(attr, access.Read)
		}))
	}
	return out, nil
}

// checkWrite 校验所有字段的写权限；返回排序后第一个无权限字段，保证错误稳定。
func (s *Service) checkWrite(caller acl.Subject, fields map[string]any) error {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	chk := access.NewChecker(s.store, caller)
	for _, k := range keys {
		if !chk.Allowed(k, access.Write) {
			return fmt.Errorf("%w: %q", ErrWriteDenied, k)
		}
	}
	return nil
}

// Materialized 返回成功物化次数（非导出计数器的只读视图）。
func (s *Service) Materialized() int { return s.materialized }
