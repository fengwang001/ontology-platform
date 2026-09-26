# 令牌桶推导与不变量

capacity=20、rate=2，初始 tokens=20、last=0。补入列括号内为未封顶理论值。

| 步 | 请求 | last | 补入 | 判定前 tokens | 判定 | 判定后 tokens |
|---|---|---|---|---|---|---|
| 1 | Allow(0,15)  | 0  | 0        | 20 | 放行 | 5  |
| 2 | Allow(0,8)   | 0  | 0        | 5  | 拒绝 | 5  |
| 3 | Allow(3,10)  | 3  | 6        | 11 | 放行 | 1  |
| 4 | Allow(3,5)   | 3  | 0        | 1  | 拒绝 | 1  |
| 5 | Allow(8,12)  | 8  | 10       | 11 | 拒绝 | 11 |
| 6 | Allow(9,6)   | 9  | 2        | 13 | 放行 | 7  |
| 7 | Allow(20,18) | 20 | 13（22 封顶） | 20 | 放行 | 2  |
| 8 | Allow(20,3)  | 20 | 0        | 2  | 拒绝 | 2  |

- (甲) 第 7 步判定前被 min 封顶成 **20**；若不封顶写成 tokens+=elapsed*rate，则 7+22=29，放行 18 后错成 **11**。
- (乙) 第 5 步正确判定后为 **11**（被拒也推进 last 并补令牌）；若只在放行路径推进时钟，该步不补令牌、last 仍为 3，tokens 停在 **1**。
- (丙) 第 6 步正确判定前 **13**（放行后 7）；若按绝对时间重算 tokens=min(20,t*rate)=min(20,18)=**18**，放行 6 后错成 **12**。

## 不变量与保证位置 / 钉住测试

1. 与朴素参照一致：`lim/limiter.go` Allow 中 `Refill(t-last)`（`tkn/bucket.go` 一次乘法+min）后 `TryConsume`，无条件推进 last；由 api 包 `TestNaiveReference`（随机序列逐步比对）钉住。
2. 令牌不越界 0≤tokens≤capacity：`tkn/bucket.go` Refill 的 min 封顶 + TryConsume 仅在 tokens≥need 时扣减；由 `TestTokensBounds` 钉住。
3. 确定性：全程无墙钟/无随机输入，状态仅由 (t,need) 序列决定（`lim/limiter.go`）；由 `TestDeterministic`（同序列双跑逐拍一致）钉住。
4. 失败不留痕：`lim/limiter.go` Allow 开头的 need、单调性校验在任何写状态之前返回哨兵错误；由 `TestFailureLeavesNoTrace`（含与对照桶比对后续行为）钉住。
