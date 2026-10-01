# stackcheck — 字节码函数栈深校验器

逐函数注册栈机指令序列，按数据流推导每条**可达**指令的入口栈深。
通过校验的函数保证：任意控制流路径上栈深一致、从不下溢、执行后栈深不超过上限
`L`（恰等于 `L` 合法），且 `RET` 的入口栈深恰为 1。被拒绝的注册不留下任何痕迹。

导入路径：`ontology/stackcheck`

```go
v, err := stackcheck.New(16)            // err 非 nil 当且仅当 L < 1
err = v.Register("fn", []stackcheck.Op{ // err 为 *stackcheck.Error，原因可机读
    {Code: stackcheck.PUSH},
    {Code: stackcheck.PUSH},
    {Code: stackcheck.ADD},
    {Code: stackcheck.RET},
})
max, qerr := v.MaxStack("fn")           // 不存在返回 ReasonNotFound

// 也可脱离注册表直接推演：
entry, maxDepth, aerr := stackcheck.Analyze(16, ops)
// entry[i] 为下标 i 的入口栈深；不可达指令为 -1
```

## 指令栈效应

函数下标从 0 起，入口栈深为 0。下表 `d` 为指令入口栈深，`d'` 为执行后栈深。

| 指令 | 入口要求 | `d'` | 后继 |
|---|---|---|---|
| `PUSH` | — | `d+1` | 下一条（落空） |
| `POP` | `d ≥ 1` | `d-1` | 下一条 |
| `ADD` | `d ≥ 2`（弹二压一，净减 1） | `d-1` | 下一条 |
| `DUP` | `d ≥ 1` | `d+1` | 下一条 |
| `JMP t` | — | `d` | 仅目标 `t`，无落空 |
| `JZ t` | `d ≥ 1`（先弹一个） | `d-1` | **先目标 `t`，后下一条**；两路都以弹后栈深进入 |
| `RET` | **`d` 恰为 1** | `d` | 无后继 |

- 执行后 `d' > L` 为超限；`d' == L` 允许。
- 落空到下标 `len(ops)` 为**落空越界**。
- 跳转目标范围对**全部指令（含不可达）**做静态检查：合法目标为 `0 … len(ops)-1`，
  等于长度或为负均非法。
- 不可达指令不做任何栈约束检查（下溢、超限、RET 栈深均不报）。

## FIFO 推导顺序

1. 工作表初始只含下标 0，入口栈深 0。
2. 按 FIFO 取出一条指令 `pc`，以其入口栈深执行：
   1. **先下溢**：`POP/JZ` 要求 `d≥1`，`ADD` 要求 `d≥2`，`DUP` 要求 `d≥1`；
   2. **后超限**：算出 `d'`，若 `d' > L` 拒绝；
   3. **再 RET**：`RET` 要求 `d == 1`；
   4. 记录 `maxDepth = max(maxDepth, d')`（仅可达指令，初值 0，故恒不小于 0）；
   5. 给后继赋入口栈深并入队：`JZ` 先目标后落空，`JMP` 仅目标，`RET` 无后继，
      其余落空到下一条；落空点等于长度即落空越界。
3. 后继已有入口栈深：相等则忽略，**不相等即为汇合不一致**（错误定位在后继下标，
   携带新到达的栈深）。

## 错误原因与优先级

`*stackcheck.Error` 含 `Reason`、`PC`、`Depth` 与消息，全部原因可区分：

注册期按下列顺序，只报第一个命中：

1. `invalid_limit` — 构造时 `L < 1`；
2. `name_exists` — 名字已注册（同名并发注册恰一个成功）；
3. `empty_program` — 指令序列为空；
4. `unknown_op` — 未知操作码，按下标从小到大找第一处；
5. `jump_out_of_bounds` — 跳转目标越界，按下标从小到大找第一处；
   **只要存在未知操作码，就先于任何跳转越界报告**；
6. 数据流错误，按 FIFO 推导中**先遇到者**报；同一条指令内
   **先下溢 → 后超限 → 再 RET 栈深不为 1**：
   - `underflow`
   - `overflow`
   - `merge_mismatch`
   - `bad_return_depth`
   - `fallthrough_out_of_bounds`

查询未注册函数返回 `not_found`。

## 并发与确定性

- `Register` 与 `MaxStack` 可并发调用；注册在注册表互斥锁内完成“查重 → 校验 →
  落表”，因此同名并发注册恰有一个成功。
- 校验在调用方切片的**副本**上进行；被拒绝的注册绝不修改函数表，调用方注册后
  修改原切片也不影响已记录结果。
- 已注册函数的最大栈深永不改变；相同的注册 / 查询序列重放结果完全相同
  （FIFO 入队顺序固定）。

## 本地验证

```bash
# 全量测试（含 -race 竞态检测、朴素推演对照、随机 2 万例）
go test -race -v ./stackcheck

# 覆盖率
go test -coverprofile=coverage.out ./stackcheck
go tool cover -html=coverage.out

# 格式化与静态检查
gofmt -l .
go vet ./...
```

测试日志逐条打印「输入（L 与指令序列）/ 输出（原因、PC、栈深、入口表、max）/
判定依据」。`TestFuzzAgainstNaive` 用独立重写的朴素 FIFO 推演
（`naive_test.go`）在随机程序上逐字段（原因、PC、栈深、入口栈深表、max）对照。
