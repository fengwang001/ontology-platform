# 权限委托组件（delegation）

允许主体把某项权限在有限有效期内委托给另一主体，并通过「可否再委托」
标记控制委托链的继续延伸。支持链式撤销（撤销后所有下游委托立即失效）、
委托环检测、有效期过期判定，并可在并发注册/求值下给出一致结果。

## 委托模型

- 委托是一条有向边 `Delegator -> Delegatee`，包含：
  - `Delegator` 委托者、`Delegatee` 受托者、`Permission` 权限；
  - `ExpiresAt` 有效期（零值表示永不过期，到期后边不再参与求值）；
  - `CanDelegate` 受托者是否可以把该权限继续委托下去。
- 根权限（原始权威）通过 `NewService` 的 root 表或 `GrantAuthority`
  播种，永不过期、不可通过委托图撤销。
- 求值（`HasPermission`）从受托者沿「未撤销、未过期」的反向边 BFS，
  只要能到达任一持有该权限的根权威即放行，判定依据为
  `root_authority` / `active_delegation_chain`。
- 权限按 `Permission` 隔离，不同权限之间互不串权。

## 再委托规则

- `CanDelegate(subject, perm)` 要求根权威可再委托，且到主体的路径上
  **每一条边**都 `CanDelegate=true`；任一中间边为 false 即返回
  `upstream_delegation_not_delegatable`。
- `Grant` 时若委托者当前「连着权威但无权再委托」，拒绝并返回
  `*RejectError{Reason: ReasonNotDelegatable}`。
- 委托者暂未持权时，边以**悬空边**接受；上游补齐后自动激活。这保证
  同一组无环委托以任意顺序注册，最终权限完全相同。

## 链式失效（撤销）

- `Revoke(id)` 标记目标边，并从其受托者出发对同权限的下游边做传递闭包，
  一次性标记全部下游边为 `Revoked`（强链式失效）。
- 撤销在写锁内完成；此后 `HasPermission` 对所有下游主体返回拒绝，依据
  `delegation_chain_revoked`；失效边也不能再被用于再委托。
- 旁支（不经过被撤销边的其他授权路径）不受影响；若下游存在另一条
  未撤销/未过期的有效路径，则仍可通过该路径持权。

## 环检测

- 每次 `Grant` 前检查：受托者能否沿同权限的「未撤销、未过期且
  `CanDelegate=true`」的边到达委托者；能到达即拒绝
  `*RejectError{Reason: ReasonCycle}`（自委托 `A->A` 同样拒绝）。
- 环检测仅在同一权限内进行；不同权限之间互不构成环。
- 所有拒绝（不可再委托 / 成环）都在校验通过前返回，**不会写入或修改
  任何委托状态**。

## 并发与顺序一致性

- `Service` 用单个 `sync.RWMutex` 保护，`Grant`/`Revoke` 原子写、
  `HasPermission`/`CanDelegate` 在读锁内对整图取一致快照。
- 求值不在缓存中保留链状态，因此并发注册与并发求值的判定始终一致。
- `TestOrderIndependence` 对同一组委托做 20 种随机打乱注册，断言最终
  权限集合逐主体完全相同。

## 审计日志

- `Logger` 接口输出三类事件：`LogGrant`（接受/拒绝 + 依据）、
  `LogRevoke`（每条被链式失效的边一行）、`LogEvaluate`（判定 + 依据）。
- 每行都包含委托者、受托者、权限以及 decision/basis；`StdLogger`
  基于标准库 `log` 输出到 stderr。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 重复执行
go test -race -count=3 ./...

# 详细输出（查看每个场景）
go test -v ./delegation

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 代码检查
gofmt -l .
go vet ./...
```

若 `go` 不在 PATH 或构建缓存目录只读，可指定：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache
```

测试覆盖：委托传递、按标记允许/禁止再委托、撤销链式失效（源头与中段）、
委托环拒绝、有效期过期与替代链、并发注册/求值、注册顺序无关性、
拒绝操作不改状态，以及拒绝原因的可区分性。
