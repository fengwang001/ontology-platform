# NOTES

## 八步推导 capacity=20 rate=2（初始 tokens=20, last=0）
| 步 | Allow | last | 补入 | 判定前 tokens | 判定 | 判定后 tokens |
|---|---|---|---|---|---|---|
| 1 | (0,15)  | 0  | 0  | 20 | 放行 | 5  |
| 2 | (0,8)   | 0  | 0  | 5  | 拒绝 | 5  |
| 3 | (3,10)  | 3  | 6  | 11 | 放行 | 1  |
| 4 | (3,5)   | 3  | 0  | 1  | 拒绝 | 1  |
| 5 | (8,12)  | 8  | 10 | 11 | 拒绝 | 11 |
| 6 | (9,6)   | 9  | 2  | 13 | 放行 | 7  |
| 7 | (20,18) | 20 | 22→封顶20 | 20 | 放行 | 2  |
| 8 | (20,3)  | 20 | 0  | 2  | 拒绝 | 2  |

(甲) 第7步判定前被 min 封顶为 **20**；若补令牌不封顶，tokens=7+22=29，放行后错成 **11**（应为2）。
(乙) 第5步正确判定后 tokens=**11**（last 推进到8）；若被拒就不推进时钟/不补令牌，tokens 停在 **1**。
(丙) 第6步正确判定前=**13**、放行后=**7**；若按绝对时间重算 min(20, 9*2)=**18**，放行后错成 **12**。

## 四条不变量
1. 与朴素参照一致：`lim/limiter.go` 的 Allow 先按 elapsed 调 tkn.Refill 再 TryConsume，顺序与参照相同；测试 `TestNaiveReferenceEquivalence` 钉住。
2. 令牌不越界 0<=tokens<=capacity：`tkn/bucket.go` Refill 用 min(capacity, …) 封顶、TryConsume 不突破0；测试 `TestTokensBounds` 钉住。
3. 确定性：状态只由显式整数 t 驱动，无 wall clock / rand，api.SelfCheck 内置序列重放两次比对；测试 `TestDeterminism` 钉住。
4. 失败不留痕：`lim/limiter.go` 先校验 need 与 t>=last，通过后才改 tokens/last；测试 `TestFailureLeavesNoTrace` 钉住。
