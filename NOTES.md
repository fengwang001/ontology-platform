# NOTES — sticky partition assignment（n=7）

七批（格式：本批配额 | 批后持有 | 本批迁移 | 累计迁移）：

1. Join(b)　配额 b:7　| b{0,1,2,3,4,5,6} | 0 | 0
2. Join(a)　配额 a:3 b:4 | a{4,5,6} b{0,1,2,3} | 3 | 3
3. Join(c)　配额 a:2 b:3 c:2 | a{4,5} b{0,1,2} c{3,6} | 2 | 5
4. Join(d)　配额 a:2 b:2 c:2 d:1 | a{4,5} b{0,1} c{3,6} d{2} | 1 | 6
5. Leave(a) 配额 b:3 c:2 d:2 | b{0,1,4} c{3,6} d{2,5} | 2 | 8
6. Join(e)　配额 b:2 c:2 d:2 e:1 | b{0,1} c{3,6} d{2,5} e{4} | 1 | 9
7. Leave(c) 配额 b:3 d:2 e:2 | b{0,1,3} d{2,5} e{4,6} | 2 | 11

(甲) 全量轮询第5批后：b{0,3,6} c{1,4} d{2,5}，与第4批 RR 结果相比 7 个分区全部易主，本批迁移 7；七批累计 0+4+4+4+7+4+6=27（正确值 11）。
(乙) 只粘性不均衡：第4批后 b 独持 0..6、a/c/d 皆 0，最大差 7；第7批后 b 仍持全部 7 个，d=e=0，最大差 7（a、c 离开时本就空手，无分区可释放，新成员永远分不到）。
(丙) +1 按 ID 升序：第2批配额 a:4 b:3，释放后 a{3,4,5,6} b{0,1,2}，本批迁移 4（正确 3），七批累计 12。正确规则第5批 +1 给 b：批前 b、c 各持 2 并列，ID b<c，d 仅持 1；无主分区 4 的缺口 b、d 均为 1，并列取 ID 最小者 b（之后 d 缺口唯一，5 归 d）。

不变量（代码保证位置 / 钉住的测试函数）：

1. 迁移最少：assign/assign.go 的 State.Rebalance——持数降序定配额、超配额从大编号释放、无主分区给最大缺口者；TestBruteForceMinimal、TestSevenBatchScenario。
2. 均衡且完整：配额之和 extra(b+1)+(k-extra)b=n，free 池按缺口被清空（k≥1）；assign/assign.go State.Rebalance；TestBalancedComplete、TestConcurrentJoins。
3. 确定性：所有并列均以 ID 升序打破（sort.Slice 与成员堆），Assignment 返回排序副本；assign/assign.go、api/api.go；TestDeterministic。
4. 失败不留痕：group/group.go 的 Group.Apply 先在成员集合副本上完成全部校验，通过后才动 assign 状态并 gen++；TestRejectedBatchNoTrace、TestSentinelErrorsDistinct。
