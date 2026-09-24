# ontology-346 分层合并 KV 写入路径
## 三、逐步推导（cap=2, fanout=2, maxLevel=1；MT=memtable，sN=段号N，`−k`=Del k）
| # | 操作 | 之后 MT | L0 | L1 | 本步合并 / Get 答案 |
|---|---|---|---|---|---|
| 1 | Put(a,1) | a1 | – | – | |
| 2 | Put(b,2) | a1 b2 | – | – | |
| 3 | Put(c,3) | c3 | s1{a1 b2} | – | flush→s1 |
| 4 | Get(c) | c3 | s1 | – | =3（MT） |
| 5 | Put(a,9) | c3 a9 | s1 | – | |
| 6 | Put(d,4) | d4 | – | s3{a9 b2 c3} | flush→s2{c3 a9}；L0 满：s1+s2→s3 |
| 7 | Del(b) | d4 −b | – | s3 | |
| 8 | Put(e,5) | e5 | s4{d4 −b} | s3 | flush→s4 |
| 9 | Get(b) | e5 | s4 | s3 | =不存在（s4 的 −b 段号4 > s3 的 b2 段号3） |
| 10 | Put(f,6) | e5 f6 | s4 | s3 | |
| 11 | Put(g,7) | g7 | – | s7{a9 −b c3 d4 e5 f6} | flush→s5{e5 f6}；L0 满：s4+s5→s6{d4 −b e5 f6}（−b 保留，s3 有 b2）；L1 满：s3+s6→s7（−b 保留，s3 有 b2） |
| 12 | Get(b) | g7 | – | s7 | =不存在（s7 的 −b） |
| 13 | Get(a) | g7 | – | s7 | =9 |
| 14 | Put(b,8) | g7 b8 | – | s7 | |
| 15 | Put(h,1) | h1 | s8{g7 b8} | s7 | flush→s8 |
| 16 | Get(b) | h1 | s8 | s7 | =8（s8 段号8 > s7 段号7） |
| 17 | Get(h) | h1 | s8 | s7 | =1（MT） |
(甲) 第9步=不存在，第16步=8。若错用「层号大者优先」，第9步会取 L1 的 s3 的 b2 而错答 2；若错成「只查 MT 和 L0」，第13步 Get(a) 会漏掉 L1 的 s7 而错答 不存在。
(乙) 若墓碑逢合并必丢：s4+s5 合并时 −b 被过早丢弃，s3 的 b2 复活进 s7，第12步错答 2（正确=不存在）。
(丙) 对调后第13步 Get(a)=1：a1 的段号大于 a9，「同键取段号最大」使后写的 a1 胜出。参照必须「按时间顺序应用」：系统唯一的全序是写入时间（段号即时间序），任意顺序应用的参照结果不唯一，无法充当一致性判据。
## 二、不变量落实（位置 / 钉住它的测试）

1. 参照一致：tree.mergeLocked 同键取段号最大+墓碑保留规则 → api_test.TestReferenceConsistency
2. 可见性=段号最大：tree.Get 取全层最大段号，RWMutex 保证快照不混代 → api_test.TestSequenceGets、api_test.TestConcurrentGet
3. 结构不变：tree.flushLocked 到 fanout 立即合并、nextID 单调递增；tree.CheckStructure → tree_test.TestStructureInvariant
4. 失败不留痕：tree.add 先校验后改状态、seg.LoadSegment 纯函数 → api_test.TestRejectedOps
