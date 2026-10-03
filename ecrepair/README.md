# ecrepair — 纠删码条带延迟修复调度器

只模拟调度决策（选哪条条带、何时启动、令牌消耗与条带终态），不做任何编码运算。
相同的操作序列重放得到完全相同的报告与状态；所有方法可并发调用，效果等价于某个串行顺序，`Tick` 是原子步骤。

## 状态定义

- 每条带 `k` 个数据分片 + `m` 个校验分片，共 `n = k+m ≤ 32` 个，编号 `0..n-1`。
- 分片状态：`Alive` / `Lost` / `Rebuilding`。
- `alive` = Alive 分片数（Rebuilding 不计入），余量 `margin = alive − k`。
- 条带状态：
  - `Healthy`：无 Lost 且无 Rebuilding；
  - `Degraded`：有 Lost 且无在途修复；
  - `Repairing`：有在途修复（即使同时仍有 Lost）；
  - `Dead`：`alive < k` 且无在途修复时判定，粘滞，其 Lost 分片不再修复。
- `firstLost`：条带从"无 Lost 且无 Rebuilding"状态首次发生丢失时的 `now`；修复完成后若仍有 Lost 则保持不变，无 Lost 时清除（`Status` 中无则返回 `-1`）。
- 有在途修复时 `alive < k` 不判 Dead（修复已读到所需数据），待完成后再判。

## Tick 四步（严格按序，原子完成）

1. **完成修复**：处理 `finish ≤ now` 的修复，按 `(finish, 条带编号)` 升序。目标分片变回 Alive；随后若 `alive < k` 且无在途修复则转 Dead；若已无 Lost 则清除 `firstLost`。
2. **令牌累积**：`tokens = min(Cap, tokens + R × (now − lastTick))`，随后 `lastTick = now`。`lastTick` 初值 0，令牌初值 0，只在成功 Tick 时累积。
3. **循环调度**，每轮：
   - 先判在途修复条带数 `≥ Q` 则停止（先于令牌判断）；
   - 候选：非 Dead、无在途修复、有 Lost，且满足 `margin ≤ tau` **或** `now − firstLost ≥ A`（恰等成立）；
   - 按 `(档位, margin, firstLost, 条带编号)` 全升序取第一条；档位 0 = 已超龄（`now − firstLost ≥ A`），档位 1 = 其余候选；
   - `tokens ≥ k` 则启动：`tokens −= k`，目标为该条带当前全部 Lost 分片（编号升序，置为 Rebuilding），`finish = now + D`；否则停止（不跳过，所有修复代价都是 k）。
4. **返回报告**：完成的条带编号列表 + 启动的修复列表（条带、目标分片、finish）。

## 错误与拒绝语义

校验先后次序：`ErrParam`（now 为负、条带编号非正、分片编号越界、构造参数非法）→ `ErrClock`（时钟倒退）→ `ErrUnknown` → `ErrDead` → `ErrNotAlive`；`AddStripe` 另有 `ErrExists`。`Tick` 只可能报 `ErrParam` 与 `ErrClock`。被拒绝的操作不改变任何状态，也不推进时钟与令牌累积基准。

## 本地验证

```bash
go test ./ecrepair/                 # 确定性用例 + 2000 组随机序列对照朴素模拟
go test -race -v ./ecrepair/        # 竞态检测；随机对照打印输入、输出与判定依据
go vet ./ecrepair/ && gofmt -l ecrepair/
```
