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

## 权限委托组件（`delegation` 包）

`delegation` 包实现带有效期与再委托标记的权限委托，位于
`delegation/delegation.go`、`delegation/graph.go`、
`delegation/register.go`、`delegation/eval.go`。

### 委托模型

- 一条委托（`Edge`）包含：委托者 `Delegator`、受托者 `Delegatee`、
  权限 `Permission`、有效期 `ExpiresAt`、可否再委托 `CanRedelegate`，
  以及内部解析出的授权来源 `ParentID`。
- 权限来源只有两种：主体直接持有根权限（`NewManager` 的 roots 或
  `GrantRoot`），或一条可沿委托边回溯到根权限的有效委托链。
- `CanRedelegate=true` 时受托者才能继续向下委托；为 `false` 时
  下游再委托一律拒绝（`REDELEGATE_FORBIDDEN`）。
- 有效期到期后该委托及其下游不再生效；到期后不能以过期授权再委托。
- 判定入口：`HasPermission(subject, permission)` 返回
  `(bool, 判定依据)`；`EffectivePermissions(subject)` 返回全部有效权限。
  判定依据形如 `ROOT_GRANT:...`、`DELEGATION_CHAIN:alice --d1(read)--> bob`，
  或拒绝原因（`REVOKED` / `EXPIRED` / `REDELEGATE_FORBIDDEN` /
  `NO_GRANT` 等）。

### 拒绝原因（可区分、且被拒绝操作不改变任何委托）

| 原因码 | 触发条件 |
| --- | --- |
| `NO_AUTHORITY` | 委托者当前不持有该权限（无有效根授权或有效来源委托） |
| `REDELEGATE_FORBIDDEN` | 来源委托标记为不可再委托仍尝试再委托 |
| `CYCLE_DETECTED` | 新委托会使同一权限的委托链成环（含自委托） |
| `ALREADY_EXPIRED` | 注册时有效期已过 |
| `DUPLICATE_ID` | 委托 ID 已存在（含批量内重复） |
| `SELF_DELEGATION` | 委托者与受托者相同 |

错误可用 `AsReject(err)` / `IsReject(err, reason)` 区分原因。
任何拒绝都会回滚，已注册委托与最终权限保持不变。

### 链式失效

- `Revoke(id)` 撤销委托时，将该边标记为撤销，并沿 `ParentID`
  向下游传播，所有直接、间接下游委托一并永久失效。
- 撤销中游委托只影响其下游：上游仍持有的权限不受影响。
- 撤销后不能再以失效边为来源注册新的委托。

### 环检测

- 注册前在同一权限的委托图上做可达性检测：若受托者沿现有委托边
  能够重新到达委托者，新边将闭合出环，直接拒绝且不入图。
- 自委托（委托者 == 受托者）视为环拒绝。
- 批量注册 `RegisterSet` 在解析全部父子关系后再统一做环检测。

### 并发与乱序一致性

- `Manager` 内部使用读写锁，注册、撤销、求值均可并发调用；
  求值期间注册/撤销不会产生撕裂视图（`go test -race` 验证）。
- `RegisterSet(edges)` 支持同一组委托以任意顺序提交：
  方法先解析依赖（按 ID 确定地选择来源）、再统一校验与提交，
  因此正序、逆序或任意顺序注册得到完全相同的最终权限；
  组内任一委托非法则整组不提交。

### 日志

注册、撤销、求值均通过可注入的日志函数输出，字段包含委托者、
受托者、权限与判定依据，例如：

```text
register ACCEPTED id=d1 delegator=alice delegatee=bob permission=read ...
evaluate subject=bob permission=read decision=ALLOW basis="DELEGATION_CHAIN:alice --d1(read)--> bob"
revoke ACCEPTED id=d1 delegator=alice delegatee=bob permission=read cascade=2
```

可用 `WithLogger(fn)` 重定向日志，传 `nil` 关闭；
`WithClock(fn)` 注入时钟以便测试有效期。

### 本地验证

```bash
# 全量测试 + 竞态检测
go test -race -v ./delegation

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

go vet ./...
gofmt -l .
```

> 若默认构建缓存目录只读，可指定 `GOCACHE=/tmp/gocache`。
