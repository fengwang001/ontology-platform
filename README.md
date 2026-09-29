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

## 逻辑删除（softdelete）

`softdelete` 包为对象提供逻辑删除（软删）能力，对象经历三种状态：

- `alive`：存活，默认查询可见。
- `soft-deleted`：仅打删除标记、记录物理保留，默认查询不可见，**仍占用唯一键**。
- `physically-deleted`：物理删除后留下的墓碑，查询不可见，**唯一键已释放**，允许同键重建。

### 操作语义

- `Create`：键被存活或软删对象占用时返回 `ErrKeyOccupied`；对象已物理删除（墓碑）或从未存在时可以同键创建。
- `SoftDelete`：仅允许 `alive -> soft-deleted`。
- `Restore`：仅允许 `soft-deleted -> alive`，清除删除标记并恢复默认查询可见；复活的是同一条记录（`CreatedAt` 不变）。
- `Purge`：物理删除并释放唯一键，记录转为墓碑。
- `Get` / `List`：默认排除软删对象；传 `QueryOptions{IncludeDeleted: true}` 可一并查到软删对象；已物理删除对象任何查询都不可见。`List` 结果按 Key 排序。

### 唯一键占用规则

- 软删不释放键：软删后以同键 `Create` 会被拒绝（`ErrKeyOccupied`），原数据与删除标记保持不变。
- 物理删除才释放键：`Purge` 后同键 `Create` 成功，得到一条全新的存活记录。
- 复活不改变键归属：复活后该键仍被同一对象占用，同键重建依旧被拒。

### 被拒绝操作的可区分原因

所有操作在一把互斥锁内完成状态判定与迁移，保证原子性；被拒绝时状态不变，错误可通过 `errors.Is` 区分：

| 场景 | 错误 |
| --- | --- |
| 对象从未存在（软删 / 复活 / 物理删除） | `ErrNotFound` |
| 对象已物理删除（软删 / 复活 / 重复物理删除 / 查询） | `ErrPhysicallyDeleted` |
| 重复软删已软删对象 | `ErrAlreadyDeleted` |
| 复活未软删（存活）对象 | `ErrNotDeleted` |
| 键仍被存活或软删对象占用时创建 | `ErrKeyOccupied` |

### 并发与复活

- 删除与复活可并发调用；每次状态迁移在锁内原子完成，不产生中间态。
- 并发软删同一对象：恰有一个成功，其余得到 `ErrAlreadyDeleted`，最终态恒为 `soft-deleted`。
- 并发复活同一对象：恰有一个成功，其余得到 `ErrNotDeleted`，最终态恒为 `alive`。
- 软删与复活混合并发：合法迁移严格交替，最终态只由两类操作的成功次数差决定（相等则 `alive`，软删多一次则 `soft-deleted`），与调度顺序无关。

### 日志

向 `New(w)` 传入 `io.Writer` 即可开启结构化判定日志（传 `nil` 静默）。每行包含对象、操作、判定结果与依据，例如：

```text
softdelete: 2026/09/29 22:40:00.123456 object="k" op=soft-delete decision=allow state=soft-deleted basis=alive -> soft-deleted, record retained and key still occupied
softdelete: 2026/09/29 22:40:00.123789 object="k" op=soft-delete decision=reject state=soft-deleted reason="softdelete: object is already soft-deleted" basis=delete marker already present
```

### 本地验证

```bash
# 软删组件全部用例（含竞态检测，覆盖并发删除/复活）
go test -race -v ./softdelete/

# 覆盖率
go test -cover ./softdelete/

# 全量测试、格式化与静态检查
go test -race ./...
gofmt -l .
go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
