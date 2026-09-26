# G 计数器 NOTES

## 六步推导（条目为 node:count，Value 为全部条目求和）

| 步 | a | b | Value("b") |
|---|---|---|---|
| S1 Set a; Inc(a,0,5) | {0:5} | 无 | 无 |
| S2 Set b; Inc(b,1,3) | {0:5} | {1:3} | 3 |
| S3 MergeInto(b,a) | {0:5} | {0:5,1:3} | 8 |
| S4 Inc(a,0,2) | {0:7} | {0:5,1:3} | 8 |
| S5 MergeInto(b,a) | {0:7} | {0:7,1:3} | 10 |
| S6 c={0:1}, d={1:1} | {0:7} | {0:7,1:3} | 10 |

(甲) S5 正确 Value(b)=10。若合并写成逐条目相加：b={0:12,1:3}，Value 错成 15（正确 10）。被重复计入的是节点 0 在 S3 已同步给 b 的那 5——a 的条目 7 里含旧值 5，相加时又计一遍。
(乙) S3 后 Value(b) 正确=8。若 Value 取所有条目的 max：max(5,3)=5，错成 5（正确 8）。
(丙) MergeInto("c","d") 后 Value(c) 正确=2。若所有节点共享一个全局计数：c、d 各 +1 落在同一槽，合并 max(1,1)=1，错成 1（正确 2）。暴露的设计必要性：必须按节点分槽计数，否则不同节点的增量在同一槽互相覆盖，max 合并无法区分「同一增量的重复传播」与「不同节点的新增量」，导致丢数。

## 四条不变量落点

1. 与朴素重算一致：`gc.Merge` 逐条目 max、`gc.Value` 逐条目求和（gc/gc.go）；测试 `gc.TestMergeNaive`、`gc.TestValueNaive`。
2. 合并是上确界（交换律+幂等）：`gc.Merge` 对两侧对称取 max（gc/gc.go）；测试 `gc.TestMergeCommutativeIdempotent`。
3. 值单调：`reg.Inc` 只加正数、`reg.MergeInto` 只取 max（reg/reg.go）；测试 `api.TestMonotonic`。
4. 失败不留痕：`reg` 先校验后写、`gc.Merge` 出错不赋值（reg/reg.go、gc/gc.go）；测试 `api.TestFailureNoTrace`。
