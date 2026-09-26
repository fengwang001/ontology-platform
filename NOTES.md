# Reservoir Sampling（算法 R）— NOTES

## 三、推导：k=3，序列 A B C D E F；注入 j：第4步=2，第5步=4，第6步=1

| 步 | 元素 | j | 动作 | 槽1 | 槽2 | 槽3 |
|---|---|---|---|---|---|---|
| 1 | A | - | 填槽 | A | - | - |
| 2 | B | - | 填槽 | A | B | - |
| 3 | C | - | 填槽 | A | B | C |
| 4 | D | 2 | 替换槽2（2≤3） | A | D | C |
| 5 | E | 4 | 丢弃（4>3） | A | D | C |
| 6 | F | 1 | 替换槽1（1≤3） | F | D | C |

- （甲）替换条件错写成 `j==1`：第4步 j=2 不替换，样本错成 A\|B\|C；正确为 A\|D\|C；**D 被错误丢弃**。
- （乙）j 范围错成 [1,k] 且无条件替换，j'=(i mod k)+1：第5步 j'=(5 mod 3)+1=3，样本错成 A\|D\|E；正确为 A\|D\|C；**E 被错误收进、C 被错误逐出**。
- （丙）正确算法下 A 存活概率 = Π_{i=4}^{6}(1−1/i) = 3/4·4/5·5/6 = **1/2**；若替换概率错成固定 k/N=1/2，则为 (1−1/2)^3 = **1/8**。

## 二、四条不变量的落点

1. **大小恒为 min(k,N)**：`rsv.Reservoir.Offer` 只按 i 填槽或整槽替换、不增删槽位，`sampler.Sampler.Sample` 只复制已填前缀；由测试 `TestSizeAndSlots` 钉住。
2. **前 k 必留且保序**：`rsv.Reservoir.Offer` 的 i≤k 分支只写槽 i−1、不触碰其他槽；由 `TestFirstKRetainedInOrder` 钉住。
3. **与朴素重放一致**：`sampler.Sampler.FeedMany` 每步仅凭当前槽与 `rng(i)` 调一次 `rsv.Offer`，不保留历史；`TestMatchesNaiveReplay` 收集全量后用同一 rng 重放第 4..N 步、逐槽比对。
4. **失败不留痕**：`api.New` 先校验容量与 rng；`sampler.FeedMany` 先整批预检空串，再在 `rsv.Reservoir.Clone` 的影子蓄水池上执行，全部成功才提交指针与计数；由 `TestRejectedBatchLeavesStateUntouched` 钉住。
