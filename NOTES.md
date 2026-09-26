# LogLog 推导与不变量

## 第三节：七步推导（m=8，rank=z+1，Add 取 max）

| 步 | (bucket,z) | rank | reg[bucket] 新值 | reg[0..7] |
|---|---|---|---|---|
| 1 | (0,0) | 1 | reg[0]=1 | 1 0 0 0 0 0 0 0 |
| 2 | (2,2) | 3 | reg[2]=3 | 1 0 3 0 0 0 0 0 |
| 3 | (2,4) | 5 | reg[2]=5 | 1 0 5 0 0 0 0 0 |
| 4 | (5,1) | 2 | reg[5]=2 | 1 0 5 0 0 2 0 0 |
| 5 | (0,3) | 4 | reg[0]=4 | 4 0 5 0 0 2 0 0 |
| 6 | (5,5) | 6 | reg[5]=6 | 4 0 5 0 0 6 0 0 |
| 7 | (2,3) | 4 | reg[2]=5（保持） | 4 0 5 0 0 6 0 0 |

- (甲) 第 6 步 rank=z+1=6，reg[5]=max(2,6)=6；若 rank 错算成 z=5，reg[5] 错成 5。
- (乙) 第 7 步 reg[2] 应保持 5；若「取 max」错成「直接覆盖」，reg[2] 错成 4，丢掉第 3 步攒下的最大 rank 5。
- (丙) 寄存器之和=15，mean=15/8=1.875；若错成只除以 3 个非零寄存器，mean 错成 5，
  2^mean 被放大 2^5 / 2^(15/8) = 2^(25/8) = 8·2^(1/8) ≈ 8.724 倍。

## 第二节：四条不变量 → 代码位置 → 钉住它的测试

1. 寄存器单调不减：`lg/lg.go` 的 `Add` 只做 `max` 不写小 → `TestMonotonic`
2. 单元素精确：`lg` 零初始化 + `Add` 只写指定 bucket → `TestSingleElementExact`
3. 与朴素重放一致：`est/est.go` 的 `Estimate` 用 `mean=Σreg/m`（含 0）→ `TestNaiveReplay`
4. 失败不留痕：`api/api.go` 的 `New`/`Add` 先校验后改状态 → `TestRejectedNoSideEffect`
