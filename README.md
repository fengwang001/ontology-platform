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

`rbac` 包实现角色层级下的权限求值，入口位于 `rbac/rbac.go`。

### 角色层级与继承

- 角色通过 `CreateRole` 注册，主体（用户/服务账号等字符串标识）通过 `AssignRole` 被赋予一个或多个角色。
- 继承关系通过 `AddInheritance(child, parent)` 建立：`child` 获得 `parent` 的全部权限；继承可多层传递（如 `admin -> editor -> viewer`），也支持菱形继承，共享祖先的权限只计一次。
- 主体最终权限 = 自身直接授权 ∪ 所在全部角色（含传递继承的祖先角色）授权的并集。

### 求值规则与直接授权优先

- `Evaluate(subject, permission)` 返回 `Decision{Granted, Sources, Reason}`：
  - 主体直接持有该权限时，来源为 `["direct"]`，直接授权优先，不再报告角色来源；
  - 否则列出所有授予该权限的可达角色（如 `["role:viewer"]`，按字典序排列）；
  - 均不存在则 `Granted=false`，`Reason` 说明拒绝依据。
- `EffectivePermissions(subject)` 返回主体全部最终权限及其来源；直接授权与角色授权同时存在时只保留 `direct`。
- 求值结果只依赖角色/授权集合：内部遍历统一排序，同一组角色与授权以任意顺序注册，判定完全相同。
- 管理器以 `sync.RWMutex` 保护，求值为只读路径，可被任意并发调用；并发求值同一主体得到逐字段一致的结果（`go test -race` 验证）。

### 被拒绝的操作与错误原因

所有错误均为可 `errors.Is` 判别的哨兵错误，且操作失败时不修改任何状态：

| 错误 | 触发场景 |
| --- | --- |
| `ErrRoleNotFound` | 赋予、授权或继承时引用不存在的角色 |
| `ErrCycle` | 自继承，或新增继承边会形成直接、间接循环 |
| `ErrDuplicate` | 重复创建角色、重复赋予角色、重复继承或重复授权 |
| `ErrGrantNotFound` | 撤销不存在的角色赋予、继承关系或权限 |

撤销接口：`RevokeRoleAssignment`、`RemoveInheritance`、`RevokeRoleGrant`、`RevokeSubjectGrant`。

### 日志

默认向 `os.Stderr` 输出结构化文本日志，可用 `SetLogger(io.Writer)` 重定向或传 `nil` 关闭。日志覆盖每一次变更与求值，包含主体、角色、权限及判定依据，例如：

```text
op=grant_role role="viewer" permission="read" result=ok reason=granted
op=evaluate subject="alice" permission="read" decision=allow basis=roles sources=[role:viewer]
op=evaluate subject="carol" permission="write" decision=deny basis=none
```

### 本地验证

```bash
# 单元测试（多层继承、直接授权覆盖、循环检测、权限并集、并发一致性）
go test -v ./rbac

# 带竞态检测（包含并发求值与并发读写用例）
go test -race -v ./rbac

# 覆盖率
go test -coverprofile=coverage.out ./rbac
go tool cover -html=coverage.out

# 格式化与静态检查
gofmt -l .
go vet ./...
```

若环境的 `GOCACHE` 位于只读目录，可先执行 `export GOCACHE=/tmp/go-cache`。
