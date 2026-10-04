# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## lessor：etcd 式租约管理器

`lessor` 包实现带 TTL 的租约：授予、续约、键挂靠、到期限速撤销，
以及主从切换时基于检查点的剩余寿命恢复。构造参数越界则整体拒绝：

| 参数 | 含义 | 合法范围 |
| --- | --- | --- |
| `MinTTL` | 最小 TTL | [1, 10^6] |
| `MaxTTL` | 最大 TTL | [MinTTL, 10^9] |
| `E` | 主从切换宽限 | [0, 10^9] |
| `R` | 每次 Tick 的撤销上限 | [1, 10^6] |
| `Kmax` | 每租约挂靠键上限 | [1, 10^6] |

节点初始为从（follower）。每个租约含有效 TTL `g`、检查点剩余 `sv`（初值 0）、
到期时刻 `x`（仅主有意义）与挂靠键集合。引擎维护时钟水位 `T`（初值 0）：
带 `now` 的操作要求 `0 <= now <= 10^15` 且 `now >= T`，成功后 `T = now`
（`TTL` 只读，不改 `T`）。所有方法可并发调用，效果等价于某个串行顺序。

### 有效 TTL 与到期判定

- `Grant(id, ttl, now)`：要求 `1 <= ttl <= MaxTTL`，有效 TTL `g = max(ttl, MinTTL)`，
  到期时刻 `x = now + g`。
- `Renew(id, now)`：要求 `x > now`，成功则 `x = now + g`、`sv` 清零，返回 `g`
  （恢复的是有效 TTL 而非授予时的 `ttl`）。
- `x <= now` 即视为已过期：`Renew`/`Attach` 报已过期，租约仍留在撤销积压中。

### Tick 的撤销次序与限速积压

`Tick(now)` 取全部 `x <= now` 的租约，按 `(x, id)` 升序只撤销前 `R` 个，
逐个返回 `(id, 键列表)`（键升序）；其余留作积压，后续 `Tick` 继续按同一顺序撤销。
到期判定走 `(x, id)` 最小堆：`Renew`/`Revoke`/`Promote` 改变到期时刻或删除租约后
堆立即修复/重建，不留惰性跳过的过期堆项；非导出计数器 `tickInspected` 记录每次
`Tick` 检视的堆顶数，恒不超过「本次撤销数 + 1」。`Revoke(id, now)` 立即撤销
（无视到期、不计入 `R`），积压中的租约同样可被 `Revoke`。

### 检查点与 Promote 的到期推导

- `Checkpoint(now)`：仅对 `x > now` 的租约写 `sv = x - now`，其余不变。
- `Demote(now)`：主变从，不改任何租约。
- `Promote(now)`：从变主，对每个租约（含积压）重算
  `x = now + E + (sv > 0 ? sv : g)`；不清 `sv`，因此再次切换仍按同一规则推导。
- `TTL(id, now)`：主返回 `max(x - now, 0)`；从返回 `sv > 0` 时的 `sv`，否则 `g`。

### 键挂靠规则

每个非空键至多挂在一个租约上。`Attach(key, id, now)` 要求租约存在且未过期：
键已在该租约上则无操作成功；该租约键数达 `Kmax` 先判满（不摘除原挂靠）；
否则把键从原租约摘除再挂到 `id`。租约被撤销（`Tick`/`Revoke`）时其键全部解除挂靠，
已移走的键不受原租约撤销影响。

### 错误判定顺序

每个操作依次判定：参数非法（`id < 1`、空 key、`ttl` 越界）→ 时间非法
（`now` 越界或小于 `T`）→ 角色错误（要求主而当前为从、`Promote` 时已是主、
`Demote` 时已是从）→ 操作自身规则（已存在 / 不存在 / 已过期 / 已满）。
被拒绝的操作不改变任何租约、键挂靠、`T` 与积压。

### 本地验证

```bash
go test ./lessor          # 单元测试 + 2000 组随机序列对照朴素模拟
go test -race ./lessor    # 竞态检测
go test -v -run TestRandomAgainstNaive ./lessor  # 查看输入/输出/判定依据日志
```

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
