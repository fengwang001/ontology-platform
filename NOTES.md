# NOTES

## 第三节：十步推导（初值全 0）

| # | 操作 | base(a,b) | delta[a] | delta[b] | Get 结果 | DeltaCount |
|---|------|-----------|----------|----------|----------|------------|
| 1 | Apply(a,+10) | 0,0 | [+10] | [] | — | 1 |
| 2 | Apply(b,+20) | 0,0 | [+10] | [+20] | — | 2 |
| 3 | Apply(a,−4) | 0,0 | [+10,−4] | [+20] | — | 3 |
| 4 | Get(a) | 0,0 | [+10,−4] | [+20] | a=6 | 3 |
| 5 | Compact() | 6,20 | [] | [] | — | 0 |
| 6 | Apply(a,+7) | 6,20 | [+7] | [] | — | 1 |
| 7 | Apply(b,−5) | 6,20 | [+7] | [−5] | — | 2 |
| 8 | Get(b) | 6,20 | [+7] | [−5] | b=15 | 2 |
| 9 | Compact() | 13,15 | [] | [] | — | 0 |
| 10 | Get(a),Get(b) | 13,15 | [] | [] | a=13,b=15 | 0 |

- (甲) 正确 Get(a)=6。按绝对值累加得 10+4=14；丢弃负 delta 得 10；钳制为非负得 10。
- (乙) 正确 Get(a)=13、Get(b)=15。若 Compact 误为 `base=Σ(delta)` 覆盖：第 9 步后 base(a,b)=(7,−5)，Get(a)=7、Get(b)=−5。
- (丙) 正确 Get(b)=15。只返回 base 得 20；只返回 Σ(delta) 得 −5。

## 第二节：四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`delta.Log.Sum` 按追加顺序累加、`store.Store.Compact` 用 `+=` 合并 → `TestNaiveReference`（随机交错序列对比朴素 map）。
2. Compaction 保值：`store.Store.Compact` 只做 `base+=Σ(delta)` 再清空日志，读路径 `base+Σ(delta)` 不变 → `TestCompactPreservesValues`。
3. 追加正确性：`store.Store.Apply` 仅向目标 Key 尾部 append，不触碰 base 与其他 Key → `TestApplyDeltaEffect`（含负数与 0）。
4. 失败不留痕：`api` 在改动前先做全部校验（`New`/`Apply` 先 validate 再 mutate）→ `TestRejectedOpsLeaveNoTrace`。

复杂度：`store.Store.Apply` 纯尾部 append，非导出字段 `lastApplyVisits` 记录访问的已有条目数（恒 0）→ `TestApplyIsTailAppend`（m=100…10000 多档断言不随 m 增长）。
