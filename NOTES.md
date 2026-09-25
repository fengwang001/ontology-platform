# 投影下推与列裁剪 — 推导与不变量

## 一、六行分步表（源列 [a,b,c,d,e]；投影 x←[a], sum←[b,c], y←[d]）

| 步骤 | 保留的源列集合 | 被裁剪的源列 |
|---|---|---|
| 定义 x←[a] 后 | {a} | {b,c,d,e} |
| 定义 sum←[b,c] 后 | {a,b,c} | {d,e} |
| 定义 y←[d] 后 | {a,b,c,d} | {e} |

| 喂入行 (a,b,c,d,e) | x | sum | y |
|---|---|---|---|
| (1,2,3,4,5) | 1 | 5 | 4 |
| (6,7,8,9,10) | 6 | 15 | 9 |
| (11,12,13,14,15) | 11 | 25 | 14 |

## 二、三问

- (甲) 按输出名 `x` 去读源列：源列无 `x`，第一行 x 会错成 0（缺省零值）或报未知列错；正确应取源列 a 的值 1。
- (乙) 只保留「直接引用列」把 b、c 裁掉：第一行 sum 读不到 b,c，错成 0（正确 2+3=5）。
- (丙) 聚合值首次计算后缓存：第二行 sum 错成 5（沿用第一行缓存，正确 7+8=15）。变更流每行是独立新值，缓存跨行复用违反「每个变更行独立求值」。

## 三、四条不变量 → 代码位置 → 钉住它的测试

1. 与全量重算一致：prune.Apply 逐输出列按 refs 现算求和；测试 TestConsistentWithFullRecompute。
2. 列裁剪精确：prune 编译时取 refs 并集、求值只读并集列（col.Row.Get 计 reads）；测试 TestPruneExactReadSet / TestReadCountIndependentOfWidth。
3. 输出稳定：prune 按投影定义顺序存 outs，OutputNames 原样返回；测试 TestOutputOrderAndNames。
4. 失败不留痕：New/SetProjection/Apply 先完整校验、通过后才改状态；测试 TestRejectedOpsLeaveStateIntact。
