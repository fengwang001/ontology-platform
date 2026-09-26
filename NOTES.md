# NOTES 一致性哈希环（vnodes=2，节点 1/2/3）

| key | H(key) | 命中后继虚节点 | 归属 |
| - | - | - | - |
| 10 | 0x2e2ac0ea | 0x3779b100 | 1 |
| 20 | 0x5c5581d4 | 0x6ef36200 | 2 |
| 30 | 0x8a8042be | 0xa66d1300 | 3 |
| 40 | 0xb8ab03a8 | 0xd5b12ab1 | 1 |
| 50 | 0xe6d5c492 | 0x0d2adbb1（回绕） | 2 |
| 60 | 0x1500857c | 0x3779b100 | 1 |
| 70 | 0x432b4666 | 0x44a48cb1 | 3 |
| 80 | 0x71560750 | 0xa66d1300 | 3 |

甲：H(50)=0xe6d5c492 大于最大虚节点 0xd5b12ab1，无「≥」者，故回绕到最小位置 0x0d2adbb1（节点2）。若不回绕而报错/返回空，则落在 (0xd5b12ab1,2^32)∪[0,0x0d2adbb1) 弧段的键（如 50）全部无归属，直接破坏不变量 1。
乙：RemoveNode(3) 后——键30（0x8a8042be）越过被删的 0xa66d1300，顺到 0xd5b12ab1（节点1）；键70（0x432b4666）越过被删的 0x44a48cb1，顺到 0x6ef36200（节点2）；键80（0x71560750）越过被删的 0xa66d1300，顺到 0xd5b12ab1（节点1）。
丙：H 是双射，H(256)=0x3779b100。错用「>」会跳过节点1 的该虚节点，归到下一个 0x44a48cb1（节点3，错）；「>=」正确归节点1。

## 不变量在代码中的保证位置与钉住测试

1. 归属确定：ring.successor 二分后越界即回绕 vnodes[0]（ring/ring.go）→ TestOwnershipDetermined。
2. 与朴素参照一致：ring.Get 取有序切片首个「pos>=H(key)」（ring/ring.go）；测试用 hashk.VNodePos 独立生成位置做线性扫描对照 → TestGetMatchesNaive。
3. 移除一致性：ring.Remove 先查成员存在，再一次性过滤该节点全部虚节点（ring/ring.go）→ TestRemoveConsistency。
4. 失败不留痕：api 各方法先做全部校验、返回哨兵错误，之后才改环；哨兵定义于 api/api.go（ErrEmptyRing/ErrNodeExists/ErrNodeNotFound/ErrInvalidVnodes）→ TestRejectedOpsNoTrace。

计数器 ring.lastChecks 为非导出字段，仅 ring 白盒测试 TestGetCheckCountLogarithmic 读其数值；ring.LookupCostBounded 对外只回布尔，不泄露数值。并发：TestConcurrentGetConsistent。
