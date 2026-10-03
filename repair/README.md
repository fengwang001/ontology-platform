# repair — 纠删码条带延迟修复调度器

只模拟调度决策（分片状态机、令牌预算、在途上限与优先级），不做任何编码运算。
所有方法可并发调用，效果等价于某个串行顺序；`Tick` 是原子步骤，外部观察者
看不到只完成了一部分修复或只启动了一部分修复的中间状态。相同的操作序列重放
得到完全相同的报告与状态。

## 接口

```go
s, err := repair.New(k, m, tau, A, R, Cap, D, Q)
err  = s.AddStripe(now, id)
err  = s.Lose(now, id, shard)
rep, err := s.Tick(now)        // rep.Completed / rep.Started
st, err := s.Status(id)        // 状态、alive、Lost、Rebuilding、margin、firstLost
tok := s.Tokens()
```

参数约束（违反即 `ErrParam`）：`1 ≤ k`、`1 ≤ m`、`n = k+m ≤ 32`、`0 ≤ tau ≤ m`、
`A ≥ 1`、`R ≥ 1`、`R ≤ Cap ≤ 1e9`、`D ≥ 1`、`Q ≥ 1`。时钟 `now` 为非负整数、
起点 0、只能不减；条带编号为正整数；分片编号 `0..n-1`。

## 状态定义

分片三态：`Alive`、`Lost`、`Rebuilding`。`alive` 为 `Alive` 分片数
（`Rebuilding` 不计入），余量 `margin = alive − k`。条带四态：

- `Healthy`：无 `Lost` 与 `Rebuilding` 分片；
- `Degraded`：有 `Lost` 且无在途修复；
- `Repairing`：有在途修复；
- `Dead`：`alive < k` 且无在途修复时判定，粘滞，其 `Lost` 分片不再修复。

有在途修复时 `alive < k` 不判 `Dead`（修复已读到所需数据），待完成后再判。
`firstLost` 是退化起点：分片丢失时若条带此前无 `Lost` 与 `Rebuilding` 分片，
则记为当前 `now`；修复完成后若已无 `Lost` 分片则清除（`Status` 返回 −1），
仍有 `Lost` 的条带保持原值。

## Tick 四步次序

1. **完成**：处理 `finish ≤ now` 的修复，按 `(finish, 条带编号)` 升序。目标分片
   变回 `Alive`；随后若 `alive < k` 且无在途修复则转 `Dead`；若已无 `Lost`
   分片则清除 `firstLost`。
2. **令牌累积**：`tokens = min(Cap, tokens + R×(now − lastTick))`，随后
   `lastTick = now`。`lastTick` 初值 0，只被成功的 `Tick` 推进。
3. **循环调度**：每轮先判在途修复条带数是否 `≥ Q`，是则停止。候选为非
   `Dead`、无在途修复、有 `Lost` 分片，且满足 `margin ≤ tau` **或**
   `now − firstLost ≥ A`（两处均恰等成立）的条带。按
   `(档位, margin, firstLost, 条带编号)` 全升序取第一个：档位 0 为
   `now − firstLost ≥ A` 的超龄条带，档位 1 为其余候选。若 `tokens ≥ k`
   则启动修复：`tokens` 减 `k`，目标为该条带当前全部 `Lost` 分片（按编号
   升序，置为 `Rebuilding`），`finish = now + D`，继续循环；若
   `tokens < k` 则停止（所有修复代价都是 `k`，不跳过另选）。同一次 `Tick`
   内启动过的条带不会再被选。
4. **报告**：返回完成的条带编号列表与启动的修复列表（条带、目标分片、
   `finish`）。

## 错误次序

被拒绝的操作不改变任何状态，也不推进时钟或令牌累积基准。判定先后：

1. `ErrParam`：`now` 为负、条带编号非正、分片编号越界、构造参数非法；
2. `ErrClock`：`now` 小于当前时钟；
3. `ErrUnknown`：条带不存在（`AddStripe` 已存在报 `ErrExists`）；
4. `ErrDead`：条带已 `Dead`；
5. `ErrNotAlive`：`Lose` 的目标分片不是 `Alive`。

`Tick` 只可能报 `ErrParam` 与 `ErrClock`。

## 本地验证

```bash
go test ./repair/                 # 全部确定性用例 + 2000 组随机对照
go test -race -v ./repair/        # 竞态检测；日志含输入、输出与判定依据
go test -run TestSpecExample ./repair/   # 跑规格书里的工作示例
```

`naive_test.go` 内含一个按规则逐条直写的朴素模拟，`TestRandomSequencesAgainstNaive`
将 2000 组随机操作序列同时重放于真实实现与朴素模拟，逐操作比对错误、报告、
每个条带的 `Status` 与令牌数。
