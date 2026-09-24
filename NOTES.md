# NOTES

## 一、九批推导（New(10)，均在组 g）
| 批 | 批后 mult>0 | distinct | 输出 / 错误 |
|---|---|---|---|
| 1 +a | a:1 | 1 | +(g,1) |
| 2 +b | a:1,b:1 | 2 | -(g,1) +(g,2) |
| 3 +a | a:2,b:1 | 2 | 无 |
| 4 -a | a:1,b:1 | 2 | 无 |
| 5 -b | a:1 | 1 | -(g,2) +(g,1) |
| 6 -b | a:1 | 1 | 拒绝：撤回不存在的值；无输出，状态不变 |
| 7 +c,-c | a:1 | 1 | 无（c 0→1→0，o=n=1） |
| 8 -a | （空） | 0 | -(g,1) |
| 9 +a | a:1 | 1 | +(g,1) |

**甲**：集合版第4批删 a：-(g,2) +(g,1)；第5批删 b：-(g,1)；第8批 a 已在第4批被删，报"撤回不存在"。正确第8批应接受并输出 -(g,1)——**第8批被误拒**。
**乙**：触组即发：多输出 6 条，第3、4、7批各多发 -(g,n) +(g,n)（n=2,2,1），违反不变量2。逐条比较时第7批发四条：-(g,1) +(g,2) -(g,2) +(g,1)，违反"按批只比批前批后、o=n 不输出"。
**丙**：[-c,+c] 第一条撤回时 c 的 mult=0 → 拒绝（撤回不存在），整批不留痕，状态同第6批后。净和校验只见 c 净值0、不判负，会错误地接受该批。九批后 View()={g:1}，正确实现共输出 **7** 条变更日志。

## 二、不变量保证位置与钉住测试
1. 与批量重算一致：dagg 按 0↔1 跨越增量维护 entries 与每组 distinct，View 取整锁快照；TestViewMatchesRecompute、TestNineBatches 钉。
2. 日志自洽：dagg.Feed 仅 o≠n 发对、o/n 为 0 发单条、同组两条相邻且按组排序；TestChangelogSelfConsistent 钉。
3. 多重性非负、无零条目：mset.M.Add/Remove 在归零时 delete，Remove 对 mult==0 返回 ok=false；TestMultiplicityNonNegative 钉。
4. 失败不留痕：dagg.Feed 先全量预校验 Sign/空串，再顺序应用并记 journal，失败逆序回滚后返回；TestRejectedBatchNoTrace 钉。
