# NOTES — Count-Min Sketch

## 第三节推导：w=6, d=3，依次 Add(2,4)、Add(5,2)、Add(11,1)

哈希：h1(x)=x%6，h2(x)=(2x+1)%6，h3(x)=(5x+3)%6。行初值全 0。

| 步 | 命中列 (h1,h2,h3) | 行1 受影响列累计 | 行2 受影响列累计 | 行3 受影响列累计 |
|---|---|---|---|---|
| Add(2,4) | (2,5,1) | col2=4 | col5=4 | col1=4 |
| Add(5,2) | (5,5,4) | col5=2 | col5=4+2=6 | col4=2 |
| Add(11,1) | (5,5,4) | col5=2+1=3 | col5=6+1=7 | col4=2+1=3 |

- (甲) Query(2)：行1 col2=4、行2 col5=7、行3 col1=4，min=**4**（=真实频数）。错写成 max 会得 **7**（被 key 2/5/11 在行2 col5 的叠加污染）。
- (乙) Add 只累加行1：行2、行3 全 0，Query(2)=min(4,0,0)=**0** < 真实 4，违反「绝不低估」——漏加的行把不存在的 0 带进了 min。
- (丙) Query 改成逐行相加：Query(2)=4+7+4=**15**。Query(5)（真实 2）正确=min(行1 col5=3, 行2 col5=7, 行3 col4=3)=**3**，被 key 11 高估（11 与 5 在三行命中列全同 (5,5,4)，每行都多算了 11 的 1）；相加版会错成 3+7+3=13。

## 四条不变量的保证位置与钉住它的测试

1. 绝不低估：Add 对每一行都累加（sketch/sketch.go Add 的 j 循环），故每行命中列 ≥ 真实频数，min 也 ≥。测试：api_test.go TestNeverUnderestimate。
2. 单键精确：无冲突时每行命中列只被该 key 累加（ch.Col 决定列，sketch.Add 只写命中格）。测试：api_test.go TestSingleKeyExact。
3. 与精确 map 参照一致：Query 取各行 min（sketch.Query），冲突只增不减。测试：api_test.go TestExactMapReference（有/无冲突两档）。
4. 失败不留痕：api.Add/Query 先校验参数（ErrBadKey/ErrBadCount/ErrBadDim），校验不过不触 sketch。测试：api_test.go TestRejectionLeavesStateUntouched、TestErrorsDistinct。
