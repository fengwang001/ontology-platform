package ontology

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrObjectTypeExists   = errors.New("object type already exists")
	ErrObjectTypeNotFound = errors.New("object type not found")
	ErrLinkTypeExists     = errors.New("link type already exists")
	ErrLinkTypeNotFound   = errors.New("link type not found")
)

// Ontology 是类型注册表，线程安全。
type Ontology struct {
	mu          sync.RWMutex
	objectTypes map[string]*ObjectType
	linkTypes   map[string]*LinkType
}

// New 创建空本体。
func New() *Ontology {
	return &Ontology{
		objectTypes: make(map[string]*ObjectType),
		linkTypes:   make(map[string]*LinkType),
	}
}

// RegisterObjectType 注册对象类型，重名或校验失败时返回错误。
func (o *Ontology) RegisterObjectType(ot *ObjectType) error {
	if err := validateObjectType(ot); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.objectTypes[ot.Name]; ok {
		return fmt.Errorf("%w: %s", ErrObjectTypeExists, ot.Name)
	}
	o.objectTypes[ot.Name] = ot
	return nil
}

// ObjectType 按名称查询对象类型。
func (o *Ontology) ObjectType(name string) (*ObjectType, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	ot, ok := o.objectTypes[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrObjectTypeNotFound, name)
	}
	return ot, nil
}

// ObjectTypes 返回全部对象类型（按注册表快照）。
func (o *Ontology) ObjectTypes() []*ObjectType {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]*ObjectType, 0, len(o.objectTypes))
	for _, ot := range o.objectTypes {
		out = append(out, ot)
	}
	return out
}

// RegisterLinkType 注册链接类型，要求两端对象类型已存在。
func (o *Ontology) RegisterLinkType(lt *LinkType) error {
	if lt == nil || lt.Name == "" {
		return errors.New("link type name is required")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.objectTypes[lt.SourceType]; !ok {
		return fmt.Errorf("%w: %s", ErrObjectTypeNotFound, lt.SourceType)
	}
	if _, ok := o.objectTypes[lt.TargetType]; !ok {
		return fmt.Errorf("%w: %s", ErrObjectTypeNotFound, lt.TargetType)
	}
	if _, ok := o.linkTypes[lt.Name]; ok {
		return fmt.Errorf("%w: %s", ErrLinkTypeExists, lt.Name)
	}
	o.linkTypes[lt.Name] = lt
	return nil
}

// LinkType 按名称查询链接类型。
func (o *Ontology) LinkType(name string) (*LinkType, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	lt, ok := o.linkTypes[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrLinkTypeNotFound, name)
	}
	return lt, nil
}

// LinkTypes 返回全部链接类型。
func (o *Ontology) LinkTypes() []*LinkType {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]*LinkType, 0, len(o.linkTypes))
	for _, lt := range o.linkTypes {
		out = append(out, lt)
	}
	return out
}

func validateObjectType(ot *ObjectType) error {
	if ot == nil || ot.Name == "" {
		return errors.New("object type name is required")
	}
	if ot.PrimaryKey == "" {
		return fmt.Errorf("object type %s: primary key is required", ot.Name)
	}
	pk, ok := ot.Properties[ot.PrimaryKey]
	if !ok {
		return fmt.Errorf("object type %s: primary key %q is not a declared property", ot.Name, ot.PrimaryKey)
	}
	if pk.Type != TypeString && pk.Type != TypeInteger {
		return fmt.Errorf("object type %s: primary key must be string or integer", ot.Name)
	}
	for name, pt := range ot.Properties {
		if pt == nil {
			return fmt.Errorf("object type %s: property %q is nil", ot.Name, name)
		}
		if pt.Name != name {
			return fmt.Errorf("object type %s: property key %q does not match name %q", ot.Name, name, pt.Name)
		}
		switch pt.Type {
		case TypeString, TypeInteger, TypeDouble, TypeBoolean, TypeTimestamp:
		default:
			return fmt.Errorf("object type %s: property %q has unknown type %q", ot.Name, name, pt.Type)
		}
	}
	return nil
}
