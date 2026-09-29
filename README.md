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

## 角色与权限继承组件（`rbac` 包）

`rbac` 包提供带角色层级的主体-权限模型，入口为 `rbac.NewManager(logger)`，所有方法均并发安全。

### 角色层级与继承

- `CreateRole(name)` 注册角色；重名返回 `ErrRoleExists`。
- `AddInheritance(child, parent)` 建立继承边，`child` 传递继承 `parent` 的全部授权（任意深度）。
- 继承图必须是无环 DAG：自继承、加入后成环的边返回 `ErrRoleCycle`，重复边返回 `ErrDuplicateInherit`；被拒绝的边不会写入，角色图保持不变。
- 引用不存在角色的继承、赋角色、授权、撤销操作均返回 `ErrRoleNotFound`。

### 授权与求值规则

- `AssignRole(subject, role)` 把主体归入角色，一个主体可有多个角色；重复赋予返回 `ErrDuplicateAssignment`。
- `GrantToRole` / `GrantToSubject` 可授予 `Allow` 或 `Deny`；同主体或同角色上同一 `resource/action` 重复授予相同判定返回 `ErrDuplicateGrant`（授予相反判定视为修改，不视为重复）。
- `Evaluate(ctx, subject)` 返回 `Decision{Allowed, Sources}`，键格式为 `resource:action`（见 `rbac.GrantKey`）；`IsAllowed` 是其便捷封装，未定义的权限一律拒绝。
- 主体最终权限 = 直接主体授权 ∪ 所在角色（含传递继承）授权的并集：
  - 直接主体授权始终优先，无论角色给出的是 allow 还是 deny（`Sources[key] == "direct"`）；
  - 多个角色对同一权限判定冲突时采用 deny-overrides，保证结果与角色/授权的注册顺序无关；
  - 角色来源记录在 `Sources` 中，作为判定依据。
- 撤销不存在的授权（包括 effect 不匹配）返回 `ErrGrantNotFound`，撤销未赋予的角色返回 `ErrAssignmentNotFound`；失败操作不改变任何状态。

### 并发语义

- 写操作持互斥锁并在提交前完成全部校验（含成环检查），失败即回滚，不产生半成品状态。
- `Evaluate` 仅持有读锁，可与其他求值并发；同一主体的并发求值得到逐字段一致的 `Decision`（已由 `-race` 下的并发测试覆盖）。
- 求值只依赖 map 键合并，不依赖 map 迭代顺序；同一组角色、继承边与授权以任意顺序注册，判定完全相同。

### 日志

每次求值逐条权限输出结构化日志（`log/slog`），字段包含 `subject`、`permission`（`resource:action`）、`basis`（`direct` 或决定该权限的角色名）与 `allowed`；角色/授权变更也有对应日志。传入 `nil` logger 时使用 `slog.Default()`。

### 本地验证

```bash
# 组件测试（含多层继承、直接覆盖、循环检测、并集、顺序无关、并发一致、日志）
go test -race -v ./rbac/

# 全量检查
gofmt -l .
go vet ./...
go test -race ./...
```
