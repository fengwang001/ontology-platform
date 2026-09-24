# ontology-370 notes

七步（注册 A=0 B=10 C=20；列：未完结 confirmed | W | W 是否变动）
1 Report(A,3)  : A3 B10 C20      | W=3    | 是
2 Report(B,12) : A3 B12 C20      | W=3    | 否
3 Report(A,8)  : A8 B12 C20      | W=8    | 是
4 Complete(B)  : A8 C20(B终=12)  | W=8    | 否
5 Report(C,25) : A8 C25          | W=8    | 否
6 Complete(A)  : C25(A终=8)      | W=25   | 是
7 Complete(C)  : 无未完结         | W=+inf | 是
(甲) 正确 3、8。错定义成 max：第1步 max(3,10,20)=20、第3步 max(8,12,20)=20，均错。
(乙) 正确 W=25。完结分区冻结值仍参与 min：min(8,12,25)=8（错）。违背「W 只对所有未完结
分区取 min」（B 第4步已移出）；完结分区永不再推进，冻结值会把 W 永久钉死，「该分区完结后
阻塞才解除」永不发生，单分区停滞阻塞语义失效。
(丙) 对调后第3个操作为 Complete(B)：未完结 A=3,C=20 → W=3（原序同位置为 8），Report(A,8)
后才到 8。W 对 Report/Complete 还满足单调不减：Report 拒回退（confirmed 只增，min 不降）
与 Complete 只把元素移出 min 集合（子集 min ≥ 全集 min）两条规则共同保证；注册新分区除外。

不变量 | 代码保证位置 | 钉住的测试函数
1 与朴素重放逐位相同：gwat.go 最小堆只放活分区、失效项懒删，Watermark 取活堆顶 | TestNaiveReplay、TestSelfCheck
2 W 单调不减：vec.go Report 拒回退；gwat.go Report 升堆项、Complete 仅移除 | TestMonotonic、TestSelfCheck
3 完结不可变/幂等不改态：vec.go done 后 Report 返 ErrCompleted，重复 Complete 空操作 | TestFinalsImmutable、TestIdempotent
4 失败不留痕：gwat.go 四类判定全部先于任何状态修改，哨兵互不相同 | TestRejectedNoTrace、TestSentinelErrors
复杂度（堆顶检查数为小常数、非导出 probeCnt）：gwat.go reMin 只沿堆顶清理失效项 | TestProbeConstant
