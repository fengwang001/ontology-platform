# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 对象类型重命名与引用更新

`ontology` 包提供并发安全的对象类型注册表 `Registry`，核心能力是原子重命名 `Registry.Rename(oldName, newName)`。

### 依赖更新规则

重命名会扫描全图依赖并同步更新所有引用旧名的位置：

- 属性引用：`Property{Type: object_ref, RefType: 旧名}`
- 链接：链接类型的 `SourceType` / `TargetType`
- Action：参数 `TypeRef`、返回值 `ReturnRefs`、属性引用 `PropertyRef.TypeName`

可用 `Registry.References(name)` 查看某类型名在全图中的所有引用位置。

### 失效规则

- 重命名成功后旧名立即失效：解析（`Resolve`）、再次重命名、重新注册（`AddType`）、作为新名复用均被拒绝，返回 `OLD_NAME_INVALIDATED`。
- 新名已被占用或新旧同名时拒绝，返回 `NAME_CONFLICT`。
- 待重命名类型未注册时返回 `TYPE_NOT_FOUND`。

### 原子性

重命名在快照副本上完成全部改写并校验后，一次性整体提交；提交后还会复检不变量，失败即整体回滚。任何一步失败都不会改变注册表状态，不会产生半改状态。可区分的拒绝原因：

- `RESIDUAL_REFERENCE`：候选状态仍残留旧名引用
- `OLD_NAME_STILL_RESOLVABLE`：候选状态中旧名仍可解析到原类型
- `DANGLING_REFERENCE`：候选状态存在指向不存在类型的悬空引用
- `RENAME_ABORTED`：提交后发现状态损坏（中断），已整体回滚

### 并发与确定性

读写通过 `sync.RWMutex` 保护，提交是单个快照替换，因此并发重命名与读取下任何一致快照中引用都不悬空（可用 `Registry.Validate()` 校验）。同一组互不冲突的重命名以任意顺序（含并发）执行，最终状态完全相同。

### 日志

重命名过程通过 `slog` 打印：类型名（`old`/`new`）、每处被更新的引用（`holder`/`kind`/`location`）以及每次判定的依据（`依据` 字段 + 错误码）。

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
