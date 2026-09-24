# NOTES — ontology-264（New(10)，仅组 g；状态只列 mult>0）
# | 批 | 批后状态 | d | 本批输出
1 | +(g,a) | a:1 | 1 | +(g,1)
2 | +(g,b) | a:1,b:1 | 2 | -(g,1) +(g,2)
3 | +(g,a) | a:2,b:1 | 2 | 无
4 | -(g,a) | a:1,b:1 | 2 | 无
5 | -(g,b) | a:1 | 1 | -(g,2) +(g,1)
6 | -(g,b) | a:1（拒绝后不变） | 1 | 拒绝：撤回不存在的值；整批无输出
7 | +(g,c),-(g,c) | a:1 | 1 | 无（c 先 1 再归 0 删除，o=n=1）
8 | -(g,a) | 空 | 0 | -(g,1)
9 | +(g,a) | a:1 | 1 | +(g,1)
甲：集合版第4批直接删 a，输出 -(g,2)+(g,1)；第5批输出 -(g,1)；第8批 a 已不在集合→报撤回不存在被拒（正确为接受并输出 -(g,1)）。本应接受却被拒：第8批。
乙：触碰即发 -o/+n 多6条：第3、4批各 -(g,2)+(g,2)，第7批 -(g,1)+(g,1)，违反不变量2；逐条比较第7批发 -(g,1)+(g,2)-(g,2)+(g,1) 四条，违反"日志按批输出、只比批前批后、o==n 不输出"。
丙：颠倒为 [-(g,c),+(g,c)]：-c 时 mult=0→撤回不存在，拒绝，状态同第6批后（a:1,d=1）；只校验净和则 c 净0不判负→误接受且无输出，漏掉非法撤回。九批后 View()={g:1}，正确共输出 7 条。
不变量 → 代码保证位置 / 钉住测试：
I1 与批量重算一致：dagg.go 提交段按 0↔1 跨越更新 distinct/entries，api.go Feed 把 outs 套用到 view。TestNineBatches、TestRandomLog、TestConcurrentWriteDisjoint
I2 日志前缀自洽（每组至多一值、- 撤的恰为现值、禁 -(G,n) 紧跟 +(G,n)）：dagg.go emit 的四条 o/n 分支与按组排序。TestRandomLog
I3 mult≥0 且 mult=0 条目即删：mset.go Remove 遇 0 拒绝、归 0 即 delete；测试白盒扫描全表。TestFaults、TestRandomLog
I4 失败不留痕：dagg.go Feed 任一错误走逆操作回滚且 outs=nil，api.go 出错直接返回不动 view/log。TestFaults
