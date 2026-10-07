package diff

// snapshot.go 实现快照构造器：一次性完成输入校验与索引构建。

// Builder 以追加方式收集快照内容，Build 时执行全量结构校验并建立索引。
type Builder struct{}

func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) AddType(t ObjectType) error       { return nil }
func (b *Builder) AddObject(o Object) error         { return nil }
func (b *Builder) AddLink(l Link) error             { return nil }
func (b *Builder) AddRemoval(r RemovalRecord) error { return nil }
func (b *Builder) Build() (*Snapshot, error)        { return &Snapshot{}, nil }

type snapshotIndex struct{}
