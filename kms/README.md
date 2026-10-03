# kms：密钥生命周期管理器

带惰性自动轮换、禁用与计划删除窗口的 KMS 密钥生命周期管理器。
所有操作可并发调用（内部一把互斥锁），结果等价于某个串行顺序；
相同操作序列重放得到完全相同的版本号、创建时刻与错误。

## 构造与全局时钟

```go
m, err := kms.NewManager(V, Wmin, Wmax, Kmax)
```

- `V`：保留版本上限，1 到 64。
- `Wmin`/`Wmax`：删除窗口下限/上限（秒），1 ≤ Wmin ≤ Wmax ≤ 10^9。
- `Kmax`：存活密钥数上限，1 到 10^6。
- 任一配置越界即以「配置非法」整体拒绝。

所有带 `now` 的操作共用一个全局时钟（已接受操作见过的最大 `now`，
`now` 取值 0 到 10^15）。`now` 小于该最大值即为「时钟回退」并被拒绝；
被拒绝的操作不推进时钟。`Describe` 同样校验回退但不推进时钟。

## 补建（惰性轮换）公式与保留版本淘汰

每个被接受的、涉及某个密钥的操作（`Encrypt`、`Decrypt`、`ReEncrypt`、
`Disable`、`ScheduleDeletion`，以及 `Describe` 的视图计算）在生效之前
先对该密钥补建。当且仅当密钥处于 `Enabled`、`P > 0` 且 `d <= now` 时：

```
c = ⌊(now − d) / P⌋ + 1          // 到期次数，d 恰等于 now 即到期
依次在 d、d+P、…、d+(c−1)P 各建一个新版本   // 创建时刻取计划时刻而非 now
d ← d + c·P
```

- 版本号从 1 起递增且永不复用，按完整的 `c` 推进（`c` 可大到 10^13 以上）。
- 实现不逐个创建：每次补建只物化最后 `min(c, V)` 个版本
  （一次空闲 10^15 秒与任何 `c ≥ V` 的空闲物化数相同，均不超过 `V`）。
- 版本数超过 `V` 时每建一个就淘汰当前最小版本号；被淘汰版本的凭据不再可解
  （报「版本已淘汰」）。
- 非 `Enabled` 的密钥不补建。`Decrypt`/`ReEncrypt` 的补建先在副本上计算，
  任一拒绝则副本丢弃，不改变任何状态。

## 状态机与 Enable 对 d 的处理

```
Create ──> Enabled ──Disable──> Disabled ──Enable──> Enabled
Enabled/Disabled ──ScheduleDeletion──> Pending ──deleteAt 到期──> 已删除
Pending ──CancelDeletion──> Disabled   // 回到 Disabled 而不是 Enabled，d 不变
```

- `Enable`（要求 `Disabled`）：若 `P > 0` 且 `d <= now`，则 `d ← now + P`，
  期间错过的轮换不补建；`d > now` 则 `d` 不变。
- `Disable`（要求 `Enabled`）：先补建再置 `Disabled`。
- `ScheduleDeletion`（要求 `Enabled` 或 `Disabled`，`w ∈ [Wmin, Wmax]`）：
  `Enabled` 时先补建，置 `Pending` 且 `deleteAt = now + w`。

## 删除时刻与世代规则

- `deleteAt <= now` 的密钥在该 `now` 下已被删除：对所有操作视同不存在，
  名额立即让出，无论是否已被物理回收（物理回收由最小堆惰性完成，
  弹出次数与存活密钥总数无关）。
- 同一 id 删除后可再次 `Create`，得到该 id 的下一个世代号（首次为 1），
  版本号重新从 1 起。
- 凭据判定：`id` 从未创建过或世代超过已创建的最大世代 → 「不存在」；
  世代不是当前存活世代（已删除或已被新世代取代）→ 「密钥已删除」。

## 拒绝类别与次序

拒绝类别可区分并带出定位信息（操作名、id、世代、版本、当前状态），
按以下次序只报第一个：

1. 参数非法（空 id、P 越界、w 越界、now 越界、凭据世代/版本小于 1）
2. 时钟回退
3. 不存在
4. 密钥已删除
5. 状态冲突（带出当前状态）
6. 状态拒绝（加解密遇 Disabled 报禁用中、遇 Pending 报计划删除中）
7. 版本问题（大于当前最大版本报「版本不存在」，小于保留的最小版本报
   「版本已淘汰」；最大/最小版本按补建之后的版本表计）
8. 超限（`Create` 使存活密钥数超过 `Kmax`）

被拒绝的操作不得改变任何密钥、版本表、`d`、`deleteAt`、世代计数与全局时钟，
也不触发补建与回收。

## 本地验证

```bash
# 全量测试（含 2000 组随机操作序列与朴素模拟的对照）
go test ./kms/

# 竞态检测 + 详细日志（打印每组操作的输入、输出与判定依据）
go test -race -v ./kms/

# 只看随机对照测试
go test -run TestRandomDifferential -v ./kms/
```
