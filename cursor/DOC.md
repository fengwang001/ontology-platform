# 可滚动结果集游标（cursor）

`cursor.Registry` 在一组固定行数（`n`，行号从 1 起）的结果集上管理命名游标，
支持 SQL 风格的滚动定位：`NEXT`、`PRIOR`、`FIRST`、`LAST`、`ABSOLUTE`、
`RELATIVE`、`FORWARD`、`BACKWARD`。

## 位置模型

每个游标有一个整数位置 `p`，取值范围恒为 `0..n+1`：

- `p = 0`：首行之前（初始位置）。
- `1 <= p <= n`：当前位于第 `p` 行。
- `p = n+1`：末行之后。

结果集为空（`n = 0`）时只有 `0` 与 `1`（即 `n+1`）两个位置。

`Position(name)` 返回当前位置。`Close(name)` 关闭并释放名字，之后可用
同名重新 `Open`。

## 单行操作的目标位置

单行类操作先计算原始目标位置 `t`，再裁剪到位置模型：

| 操作 | 目标位置 `t` |
| --- | --- |
| `NEXT` | `p + 1` |
| `PRIOR` | `p - 1` |
| `FIRST` | `1` |
| `LAST` | `n` |
| `ABSOLUTE k` | `k > 0` 时为 `k`；`k < 0` 时为 `n + 1 + k`（倒数定位，`-1` 即最后一行）；`k = 0` 时为 `0` |
| `RELATIVE k` | `p + k`（`k` 可为负） |

裁剪与返回规则：

- `t < 1`：位置置为 `0`，不返回行。
- `t > n`：位置置为 `n+1`，不返回行。
- 否则：位置置为 `t`，返回第 `t` 行（返回值即行号）。

`NEXT`、`PRIOR`、`FIRST`、`LAST` 忽略参数 `k`。

因此：从 `n+1` 执行 `PRIOR` 得到第 `n` 行；`NEXT` 越过末行到 `n+1`
后再 `PRIOR` 仍得到第 `n` 行；`RELATIVE 0` 在行上重复当前行，在 `0`
或 `n+1` 边缘不返回行且位置不变。

## 多行操作

- `FORWARD k`（`k > 0`）：从第 `p+1` 行起升序返回至多 `k` 行，不越过
  第 `n` 行。返回行数不足 `k`（含一行都没有）时位置置为 `n+1`；恰好
  返回 `k` 行时位置置为最后返回行的行号。
- `BACKWARD k`（`k > 0`）：从第 `p-1` 行起降序返回至多 `k` 行，不越过
  第 1 行。返回行数不足 `k` 时位置置为 `0`；否则位置置为最后（最小）
  返回行的行号。

无行可返回的合法 `Fetch` 仍然按上述规则移动位置；只有被整体拒绝的
操作才保持位置不变。

## 只进游标

`Open(name, n, false)` 打开只进游标，只允许：

- `NEXT`
- `FORWARD`
- `RELATIVE` 且 `k >= 0`

`PRIOR`、`FIRST`、`LAST`、`ABSOLUTE`、`BACKWARD` 以及负向 `RELATIVE`
一律以 `forward_only` 原因拒绝。

## 拒绝原因与顺序

所有错误均为 `*cursor.Error`，按下列顺序只报第一个，且被拒绝的操作
不改变位置：

- `Open`：`empty_name`（名字为空）→ `negative_rows`（`n < 0`）→
  `rows_too_large`（`n > 2^40`）→ `name_exists`（名字已打开）。
- `Fetch`：`not_found`（游标不存在）→ `invalid_op`（操作不在所列之内）→
  `forward_only`（只进游标上的不允许操作）→ `non_positive_count`
  （`FORWARD`/`BACKWARD` 的 `k <= 0`）。
- `Position`、`Close`：`not_found`。

## 整数精确性

所有位置运算都经过溢出检查，`k` 取 `math.MaxInt64` / `math.MinInt64`
时不发生回绕：正向溢出按越过末行处理（位置 `n+1`），负向溢出按越过
首行处理（位置 `0`）。

## 并发

注册表用一把互斥锁保护名字表，每个游标另有自己的互斥锁：不同游标上的
操作可以并行；同一游标上的并发 `Fetch` 被串行化，等价于某个串行顺序，
位置任何时刻都在 `0..n+1` 之内。操作不依赖外部可变状态，相同操作序列
重放得到完全相同的返回行与位置。

## 本地验证

```bash
# 全量测试（含 2000 组随机序列对拍、边界用例、并发与重放测试）
go test ./cursor -v

# 竞态检测
go test -race ./cursor

# 只跑 2000 组随机对拍
go test ./cursor -run TestDifferentialRandom -v
```

测试内置一个按规则逐步实现的朴素模拟器（`math/big` 精确整数），与真实
实现逐步对拍；`-v` 日志中逐条打印输入参数、两侧返回行、拒绝原因、
两侧位置以及 `MATCH`/`MISMATCH` 判定依据。
