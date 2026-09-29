# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 外键约束维护器（`fk` 包）

`fk.Maintainer` 消费变更流上的父表 / 子表变更事件，保证任意时刻
子表引用的父行始终存在。所有写操作先校验后落库，校验失败整体拒绝，
不改变视图、父表与子表；读操作（`ParentView` / `ChildView` / `RefCount` /
`SelfCheck`）可并发调用，视图按 ID 排序且逐字段深拷贝，同一实例并发
读取结果一致。

### 四类操作的判定顺序

- `InsertParent`：父行已存在则幂等成功（不改写既有字段与引用计数）；
  否则插入并初始化引用计数为 0。
- `DeleteParent`：先查存在性，不存在拒绝（`PARENT_NOT_FOUND`）；
  再查引用计数，大于 0 拒绝（`PARENT_REFERENCED`）；均通过才删除。
- `InsertChild`：先查父行当前是否存在，不存在拒绝（`PARENT_MISSING`）；
  再查子行是否已存在，已存在则幂等成功；否则插入并将父行引用计数加 1。
- `DeleteChild`：先查存在性，不存在拒绝（`CHILD_NOT_FOUND`）；
  存在则删除并将父行引用计数减 1。

### 被拒子行不记忆

`InsertChild` 因父行不存在被拒绝时，维护器不留下任何记录（无墓碑、
无待决队列）。该子行需由上游在父行到达后重投；重投时按正常插入流程
重新判定。因此删除一个"曾被拒"的子行会得到 `CHILD_NOT_FOUND`。

### 本地验证：重放成功操作核对结果

1. 对变更流逐条调用 `Apply`，记录返回 `nil` 的成功操作序列，
   每条之后可调用 `SelfCheck` 校验不变量（引用计数恰等于实际被引用
   子行数，且无子行指向不存在的父行）。
2. 用 `New()` 新建实例，仅重放成功操作序列。
3. 比较两个实例的 `ParentView` / `ChildView`，逐字段相等即结果可复现。

参考实现见 `fk/maintainer_test.go` 的 `TestReplayDeterminism`。
