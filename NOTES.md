# 蓄水池采样（算法 R）推导与不变量

## 第三节：六行分步表（k=3；注入 j：第4步=2、第5步=4、第6步=1）

```
步  元素  j   动作      样本(槽1|槽2|槽3)
1   A     -   填槽1     A|-|-
2   B     -   填槽2     A|B|-
3   C     -   填槽3     A|B|C
4   D     2   替换槽2   A|D|C
5   E     4   丢弃      A|D|C
6   F     1   替换槽1   F|D|C
```

- (甲) 替换条件错写成 `j==1`：第4步 j=2≠1，D 被错误丢弃，第4步后样本错成 A|B|C，正确应为 A|D|C。
- (乙) j 错取 [1,k] 且无条件替换（j'=(i mod k)+1）：第5步 j'=(5 mod 3)+1=3，E 顶替槽3，样本错成 A|D|E，正确应为 A|D|C；E 被错误收进、C 被错误逐出。
- (丙) 正确算法下 A 存活概率 = (1-1/4)(1-1/5)(1-1/6) = 3/4×4/5×5/6 = 1/2。若替换概率错成固定 k/N=1/2：每步 A 被逐概率 (1/2)(1/3)=1/6，存活概率错成 (5/6)³ = 125/216。

## 第二节：四条不变量的保证位置与钉住测试

1. 大小恒 min(k,N)：`rsv.Step` 只在 i<=k 填槽、j<=k 替换，`sampler.n` 精确计数已见元素；测试 `TestSampleSize`。
2. 前 k 必留且保序：`rsv.Step` 的 i<=k 分支把 e_i 写入槽 i；测试 `TestFirstKRetained`。
3. 与朴素重放一致：`sampler.feedAll` 与测试内 `naiveReplay` 用同一 rng 做同一判定；测试 `TestMatchesNaiveReplay`。
4. 失败不留痕：`sampler.feedAll` 先校验全部元素与全部 rng 抽取、通过后才改状态；测试 `TestFailureLeavesNoTrace`。
