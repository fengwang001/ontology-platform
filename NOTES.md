# 一致性哈希环：推导与不变量
v=2，节点 1/2/3；H(x)=x·2654435761（uint32 回绕）；虚节点位置 H(nodeID·256+i)，后继=首个位置 ≥ H(key) 的虚节点，全无则回绕到最小者。
| 键 | H(键) hex | 命中后继虚节点 | 归属节点 |
| 10 | 0x2e2ac0ea | 0x3779b100 | 1 |
| 20 | 0x5c5581d4 | 0x6ef36200 | 2 |
| 30 | 0x8a8042be | 0xa66d1300 | 3 |
| 40 | 0xb8ab03a8 | 0xd5b12ab1 | 1 |
| 50 | 0xe6d5c492 | 0x0d2adbb1 | 2（回绕）|
| 60 | 0x1500857c | 0x3779b100 | 1 |
| 70 | 0x432b4666 | 0x44a48cb1 | 3 |
| 80 | 0x71560750 | 0xa66d1300 | 3 |
（甲）H(50)=0xe6d5c492 大于最大虚节点 0xd5b12ab1，不存在 ≥ 它的虚节点，故回绕到位置最小的 0x0d2adbb1（节点2）；若不回绕而报错/返回空，则高段 (0xd5b12ab1, 2^32) 的键（H 为双射，确定非空）全部无归属，合法键 Get 失败，不变量1被破坏。
（乙）RemoveNode(3) 摘除 0x44a48cb1、0xa66d1300 后：键30 H=0x8a8042be → 首个 ≥ 者 0xd5b12ab1（节点1）；键70 H=0x432b4666 → 0x6ef36200（节点2）；键80 H=0x71560750 → 0xd5b12ab1（节点1）。
（丙）键256：H(256)=2654435761·256=0x3779b100，恰是节点1的虚节点；用 >= 正确归节点1。错用 > 会跳过自身到下一个 0x44a48cb1（节点3），正确1、错归3。
不变量（代码保证位置 / 钉住的测试函数）：
I1 归属确定：ring.Successor 在有序切片上 sort.Search 命中真实挂入虚节点，空环由 api.Get 提前返回 ErrEmptyRing；TestGetReturnsRealNode。
I2 与朴素参照一致：ring.Successor 的 `>=` 单调谓词加 idx==len 回绕取 [0]，与排序后线性扫描（含回绕）逐条等价；TestGetMatchesNaive、TestDerivationEightKeys。
I3 移除一致性：ring.RemoveNode 重建切片剔除该 node 的全部 v 个虚节点并 delete 成员表，之后任何键都不可能取到它；TestRemoveNodeConsistency。
I4 失败不留痕：api.New/AddNode/RemoveNode/Get 先校验后改状态（ErrInvalidVnodes/ErrNodeExists/ErrNodeMissing/ErrEmptyRing 四个互不相同哨兵），ring 层重复 Add/缺失 Remove 直接返回 false 不动切片；TestRejectedOpsLeaveStateUnchanged、TestSentinelErrorsDistinct。
复杂度：ring.Ring.cmp（非导出）记录最近一次 Successor 的谓词比较次数，TestGetComparisonCountSublinear 在 m=100..10000 多档断言 cmp≤⌈log₂m⌉+1；只读一致性由 TestConcurrentGetConsistent 钉住（go test -race）。
