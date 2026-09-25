# NOTES

## 三、推导（R=5，窗口 [0,10)）
| 步 | Key | 该 Key 存活候选 (Value,TS,Del) | 本条判定 |
|---|---|---|---|
| 1 | a | (1,1,put) | 新存活 |
| 2 | b | (10,2,put) | 新存活 |
| 3 | a | (2,4,put) | 新存活，(1,1) 被折叠 |
| 4 | c | (5,3,put) | 新存活 |
| 5 | b | (0,5,del) | 新存活，(10,2) 被折叠 |
| 6 | a | (0,6,del) | 新存活，(2,4) 被折叠 |
| 7 | c | (0,7,del) | 新存活，(5,3) 被折叠 |
| 8 | a | (3,8,put) | 新存活，(0,6) 被折叠 |
| 9 | d | (7,6,put) | 新存活 |
| 10 | d | (7,6,put) 不变 | TS=2<6，(9,2) 被折叠丢弃 |

保留期：a put→留；b 5+5=10<=10→弃；c 7+5=12>10→留；d put→留。输出（Key 升序）：`a(3,8,put) c(0,7,del) d(7,6,put)`，b 无输出。

- (甲) 错把「最后到达」当最新：d 存活错成 (9,2,put)，输出 Value 错成 9（正确为 7）。
- (乙) 丢弃条件错写成严格 `TS+R<hi`：10<10 不成立，b 的墓碑被错保留（正确：丢弃，b 无输出）。
- (丙) 只删墓碑却留下更早 put：b 错输出 (10,2,put)；正确：b 无任何输出（不复活）。

## 二、不变量落点（代码位置 ← 钉住它的测试）

1. 朴素一致：`compact.Engine.Compact` 每次对窗口全量重算折叠 ← `TestCompactMatchesNaive`
2. 无重复 Key：折叠用 `map[string]rec.Rec`，每 Key 至多一格 ← `TestNoDuplicateKeys`
3. 不复活：折叠只留 TS 最大者，墓碑被弃则该 Key 整组无输出 ← `TestNoResurrection`
4. 失败不留痕：`Feed` 先全量校验再写入、`Compact` 先验参数再动手 ← `TestRejectStateIntact`
并发安全：api 读写锁 + compact 内原子计数器 ← `TestConcurrentCompact`
