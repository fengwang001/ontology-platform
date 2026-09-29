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

## 对象类型重命名（`ontology` 包）

`Store.RenameObjectType(oldName, newName)` 实现对象类型重命名与全图引用同步更新。

**依赖更新范围（扫描全图）**
- 属性引用：`ObjectType.Properties[].TypeRef`
- 链接引用：`ObjectType.Links[].SourceType` 与 `TargetType`
- Action 引用：`Action.Params[].TypeRef`、`Inputs`、`Creates`、`Reads`、`Updates`、`Deletes`

以上每个引用旧名的位置都会被改写为新名；类型以稳定的 `RID` 标识，重命名只改 `Name`。

**失效规则**
- 重命名提交后旧名立即失效：`Graph.Resolve` / `Graph.Lookup("旧名")` 返回不存在，绝不回指到重命名后的类型。
- 不保留旧名别名；再次以旧名发起重命名会被拒绝（`TypeNotFound`）。

**原子性与中断恢复**
- 重命名在深拷贝的暂存图上进行（改名 + 引用更新 + 校验），全部通过后才以不可变快照原子替换发布；读操作永远只看到旧快照或新快照，引用始终不悬空。
- 提交前失败（测试中通过 failPoint 注入）整体回滚，已发布图与 RID 均不变，且可安全重试。
- 暂存阶段会在图上写入 pending 标记。重新加载图时：原状态完好则自动回滚，改名与引用均已完成则自动补提交，两者都不满足则按半改状态拒绝加载。

**拒绝原因（`errors.Is` 可区分）**
- `ErrTypeNotFound`：旧名不存在（含旧名已失效后再次使用）。
- `ErrNameConflict`：新名与现有类型重名。
- `ErrResidualOldName`：重命名后仍有引用位置残留旧名。
- `ErrStaleName`：旧名重命名后仍可解析到原类型。
- `ErrDanglingReference`：图中存在指向不存在类型的引用。
- `ErrHalfApplied`：重命名中断产生的半改状态。

被拒绝的操作不会改变任何类型；每个错误都带有判定依据（Detail），日志使用 `slog` 打印类型 RID、新旧名、每个被更新的引用位置（如 `property.type{type=r.user,property=manager}`）、引用计数与判定原因。

**并发语义**
- 重命名互斥串行化提交，读取基于 `atomic.Pointer[Graph]` 快照，无需加锁。
- 同一组重命名相互独立、可交换，以任意顺序（含并发）得到完全相同的最终状态。

**本地验证**
```bash
go test -race -v ./ontology
go test -race -count=20 ./ontology   # 反复验证并发场景
go test -cover ./ontology
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
