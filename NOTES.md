# NOTES

## 推导（m=8，七步；数组写法 reg0|reg1|…|reg7）

| 步 | (bucket,z) | rank=z+1 | reg[b]新值 | 该步后八个寄存器 |
|---|---|---|---|---|
| 1 | (0,0) | 1 | max(0,1)=1 | 1\|0\|0\|0\|0\|0\|0\|0 |
| 2 | (2,2) | 3 | max(0,3)=3 | 1\|0\|3\|0\|0\|0\|0\|0 |
| 3 | (2,4) | 5 | max(3,5)=5 | 1\|0\|5\|0\|0\|0\|0\|0 |
| 4 | (5,1) | 2 | max(0,2)=2 | 1\|0\|5\|0\|0\|2\|0\|0 |
| 5 | (0,3) | 4 | max(1,4)=4 | 4\|0\|5\|0\|0\|2\|0\|0 |
| 6 | (5,5) | 6 | max(2,6)=6 | 4\|0\|5\|0\|0\|6\|0\|0 |
| 7 | (2,3) | 4 | max(5,4)=5 | 4\|0\|5\|0\|0\|6\|0\|0 |

- (甲) 第6步 rank=6，reg[5]=max(旧2,6)=6。若漏 +1 错算 rank=z=5，则 reg[5]=max(2,5)=5，比正确值少 1。
- (乙) 第7步 reg[2] 必须保持 5。若写成直接覆盖，reg[2] 错成 4，丢掉第3步 (2,4) 观测到的 rank=5（该桶最大前导零证据丢失）。
- (丙) 正确 mean=15/8=1.875（除以含 0 的全部 m=8 个）。若只除以 3 个非零寄存器：mean=15/3=5。2^mean 放大倍数 = 2^5/2^1.875 = 2^(25/8) = 8·2^(1/8) ≈ 8.724 倍。

## 不变量（保证位置 / 钉住的测试）

1. 寄存器单调不减：lg/lg.go 的 `Registers.Add` 仅在 z+1 更大时赋值；api 校验先于该调用。测试 `TestAddMonotonic`（api/api_test.go）。
2. 单元素精确：lg.Add 只写 bucket 一格，其余初始为 0；api.New 零值初始化。测试 `TestSingleElementExact`。
3. 与朴素重放一致：est/est.go 的 `Estimate` 对 m 格快照逐项求和代 mean 公式；逐格 max 在 lg.Add。测试 `TestNaiveReplay`（含随机序列与 Estimate 复算）。
4. 失败不留痕：api/api.go 的 `Add` 先全部校验（ErrInvalidM 仅在 New；ErrBucketOutOfRange、ErrInvalidZ）再调用 est，被拒不触达任何寄存器。测试 `TestRejectedLeavesNoTrace`；三类互异由 `TestSentinelErrorsDistinct` 钉住。
