# NOTES

## 八步推导：A=[5,2,8,1,9,3,7,4]（区间左闭右开）
| 步 | 操作 | 步后结果 |
|---|---|---|
| 1 | Query(0,8) | 1 |
| 2 | Update(0,10) | A=[10,2,8,1,9,3,7,4] |
| 3 | Query(0,4) | 1 |
| 4 | Update(3,11) | A=[10,2,8,11,9,3,7,4] |
| 5 | Query(0,4) | 2 |
| 6 | Query(4,8) | 3 |
| 7 | Update(2,0) | A=[10,2,0,11,9,3,7,4] |
| 8 | Query(0,8) | 0 |

(甲) 误当闭区间 [0,2]：min(A0,A1,A2)=min(10,2,0)=**0（错值）**；正确半开 [0,2)=min(10,2)=**2**。
(乙) Update(3,11) 后只改叶不重算祖先：[0,4) 节点保留旧 min=**1（陈旧错值）**；正确值 **2**。
(丙) Query(3,3)=**+Inf=MaxInt64**；错成“返回 A[3]”得 **11**。n 非 2 次幂（如 n=5 补到 8）时补齐叶若误填 0/MinInt64，跨边界祖先与根会被拉成该值（根恒为 0/MinInt64）；填 +Inf 才是 min 单位元，任何覆盖补齐位的节点 min 不变。

## 第二节四条不变量：保证位置 / 钉住的测试
1. 与朴素参照一致：`rmq.Build` 令 Node[i]=min(左子,右子)，`rmq.Structure.Query` 只把 [l,r) 分解成互不相交的覆盖节点取 min —— `TestNaiveAgreement`
2. 更新后仍正确：`rmq.Update` 改叶后经 `seg.Parent` 逐层重算直到根，并同步镜像数组 —— `TestUpdatePropagation`
3. 单位元无污染：`seg.NewTree` 全部节点预填 Inf，`rmq.Build` 只 copy n 个真叶，补齐位保持 Inf —— `TestPaddingIdentity`
4. 失败不留痕：`rmq.Query`/`rmq.Update` 全部校验先于任何写入，空数组在 `rmq.Build` 直接拒绝 —— `TestRejectedOpsNoStateChange`
