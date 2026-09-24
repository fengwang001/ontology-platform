# CDC 确定性令牌化脱敏 — 推导与不变量

配置：users.email 与 orders.buyer_email→域 email；users.phone→域 phone；orders.amount 非敏感透传；maxTokens=6。

| # | 事件 | 判定 | Before 敏感列 | After 敏感列 | 条目数 |
|---|---|---|---|---|---|
| 1 | users I | 接受 | — | email#1(a), phone#1(111) | 2 |
| 2 | orders I | 接受 | — | email#2(b)；amount=9 透传 | 3 |
| 3 | orders U | 接受 | email#2(b)；amount=9 | email#2(b)；amount=12 | 3 |
| 4 | users U | 接受 | email#1(a), phone#1(111) | email#3(c), phone=NULL | 4 |
| 5 | orders U | 接受 | email#2(b) | email#4("") | 5 |
| 6 | users I | 拒绝 ErrTokenLimit | — | d 先得 email#5，222 将成第 7 条>6，整事件回滚 | 5 |
| 7 | users I | 接受 | — | email#5(e), phone=NULL | 6 |

(甲) 正确：第3步 Before.buyer_email=email#2，第4步 Before.email=email#1。若 Before/After 各建一张表各自从 1 编号，二者都会错成 email#1，使不同原值 a 与 b 在下游看起来相同。
(乙) 若 NULL 也占令牌：第4步 After.phone 错成 phone#2；额度被 NULL 占用后第7步 e 将成第7条，第7步被错误拒绝（ErrTokenLimit，正确实现应接受）。
(丙) 第6步 d 为第6条尚可、222 将成第7条>6，故整事件拒绝并回滚 d，条目数仍为 5。若不回滚（d=email#5 残留、size=6），第7步 e 将成第7条而被 ErrTokenLimit 错误拒绝。正确实现下第7步接受，After.email=email#5。

## 四条不变量：代码位置 + 钉住的测试

1. 同值同令牌、一一对应、编号 1..k 连续：tokn.go 的 Tx.Get（命中直接返回，新值按 len+1 分配）；钉于 TestSevenEvents、TestNaiveReference、TestConcurrency。
2. 与朴素批量参照一致：mask.go 的 Mask 固定按 事件→Before→After→列名字节序（sortedKeys）遍历分配；钉于 TestNaiveReference（随机序列对拍朴素参照）。
3. 结构保持、不改输入：mask.go 输出用新 map 浅拷贝、nil 指针原样保留、非敏感列与 Table/Op 原样复制；钉于 TestSevenEvents、TestInputNotMutated。
4. 失败不留痕：tokn.go Tx.rollback 删除本事件全部新条目并恢复 total；非法配置/未知表/形状错误在 mask 分配前返回；钉于 TestRejectedLeavesState（四类错误）与 TestSevenEvents（第6步）。

自检：api.go SelfCheck 内置上述序列逐条核验四条；钉于 TestSelfCheck。超限判定在 tokn.go Tx.Get（total+1>max）；查找为 O(1) map 直接定位，计数为 tokn 非导出字段，仅 tokn 包内 TestLookupCounter 白盒断言其不随 m 增长。
