# NOTES — G 计数器推导与不变量

## 六步推导表（条目写作 {node:count}，缺失视为 0）

| 步 | 操作 | a | b | Value("b") |
|---|---|---|---|---|
| S1 | Set(a,{}); Inc(a,0,5) | {0:5} | 无 | 无 |
| S2 | Set(b,{}); Inc(b,1,3) | {0:5} | {1:3} | 3 |
| S3 | MergeInto(b,a) | {0:5} | {0:5,1:3} | 8 |
| S4 | Inc(a,0,2) | {0:7} | {0:5,1:3} | 8 |
| S5 | MergeInto(b,a) | {0:7} | {0:7,1:3} | 10 |
| S6 | Set(c,{});Inc(c,0,1); Set(d,{});Inc(d,1,1) | c={0:1}, d={1:1} | — | — |

- (甲) S5 正确 Value("b")=10。若合并逐条目相加：b={0:5+7=12,1:3}，错成 15（正确 10）。被重复计入的是节点 0 在 S3 已并入 b 的那段计数 5。
- (乙) S3 后 Value("b") 正确为 8（5+3 求和）。若 Value 取条目 max：错成 max(5,3)=5（正确 8）。
- (丙) S6 后 MergeInto(c,d)：c={0:1,1:1}，Value("c")=2。若全局共享一个计数：c=d=1，max 合并得 1，错成 1（正确 2）。暴露的设计必要性：必须按节点分别计数，否则不同节点的并发增量在 max 合并下互相覆盖丢失。

## 四条不变量：保证位置与钉住它的测试

1. 与朴素重算一致：`gc.Merge` 逐条目取 max、`gc.Value` 逐条目求和（gc/gc.go）；测试 `TestMergeMatchesNaive`、`TestValueMatchesNaive`。
2. 合并是上确界：`gc.Merge` 对并集逐条目取 max，天然交换且幂等（gc/gc.go）；测试 `TestMergeCommutativeIdempotent`。
3. 值单调：`Inc` 只加正数、`MergeInto` 逐条目 max 不降（reg/reg.go 委托 gc）；测试 `TestValueMonotonic`。
4. 失败不留痕：`gc.Counter.Inc`/`gc.Merge`/`gc.FromMap` 先校验后写入，校验失败零副作用（gc/gc.go、reg/reg.go）；测试 `TestFailureLeavesNoTrace`。
