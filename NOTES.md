# 加权随机采样（A-Res）推导与不变量

## 第三节推导：k=2，key = U^(1/w)

各元素 key：A=0.9^1=0.9，B=0.64^(1/2)=0.8，C=0.75^1=0.75，D=0.343^(1/3)=0.7，E=0.1^1=0.1。

| 步 | 元素 | key | 动作 | 该步之后样本（元素:key） |
|---|---|---|---|---|
| 1 | A | 0.9 | 纳入（<k） | A:0.9 |
| 2 | B | 0.8 | 纳入（<k） | A:0.9, B:0.8 |
| 3 | C | 0.75 | 丢弃（0.75 < 最小 key 0.8） | A:0.9, B:0.8 |
| 4 | D | 0.7 | 丢弃（0.7 < 0.8） | A:0.9, B:0.8 |
| 5 | E | 0.1 | 丢弃（0.1 < 0.8） | A:0.9, B:0.8 |

- (甲) key=w：按权重留 top-2，D(3) 顶替 A(1)，错成 {B, D}；正确 {A, B}。A 被错误逐出，D 被错误收进。
- (乙) key=U：C(0.75) 顶替 B(0.64)，错成 {A, C}；正确 {A, B}。根因：权重 2 的 B 因少做 1/w 次幂（0.8>0.75 变成 0.64<0.75）被 C 挤掉。
- (丙) 保留 key 最小：逐步留最小两个，错成 {D, E}（D:0.7, E:0.1）；正确 {A, B}。

## 四条不变量：保证位置与钉住它的测试

1. 大小恒为 min(k,N)：wsampler.Feed 仅在纳入/替换后同步 kept=res.Len()；wrs.Consider 容量满后只替换不增长。测试 TestSampleSizeMinKN。
2. 前 k 必留：wrs/wrs.go Consider 中 `len(slots) < k` 分支无条件纳入。测试 TestFirstKAllRetained。
3. 与朴素离线参照一致：wrs.Consider 的「大于当前最小 key 才替换」规则等价于离线 top-k。测试 TestMatchesOfflineReference。
4. 失败不留痕：wsampler.Feed 先整批校验（值/权重/预抽全部 U_i）再改任何状态。测试 TestRejectedFeedLeavesStateUntouched。
