# ontology-327 推导与不变量
记号: k:值@v=存活行; T k@v=墓碑; G=水位; R=10,maxKeys=100,每事件单独成批
步 事件         cur 决   G后 清除   存活行                 墓碑
1  U k1 v5 a   0   应用 5   无    k1:a@5                 -
2  D k1 v8     5   应用 8   无    -                      T k1@8
3  U k1 v6 b   8   忽略 8   无    -                      T k1@8
4  U k2 v12 c  0   应用 12  无    k2:c@12                T k1@8
5  U k1 v8 d   8   忽略 12  无    k2:c@12                T k1@8
6  D k3 v15    0   应用 15  无    k2:c@12                T k1@8;T k3@15
7  U k2 v18 e  12  应用 18  k1@8  k2:e@18                T k3@15
8  U k1 v7 f   0   应用 18  无    k1:f@7;k2:e@18          T k3@15
9  U k3 v14 g  15  忽略 18  无    k1:f@7;k2:e@18          T k3@15
10 U k4 v30 h  0   应用 30  k3@15 k1:f@7;k2:e@18;k4:h@30  -
甲: 第5步忽略(8≯8,严格大于)。若条件写成>=: 第5步被应用,k1=d@8且墓碑消失,第8步7<8再被忽略,最终k1=d@8(正确实现最终k1=f@7)。
乙: 删除若不留墓碑: 第3步cur=0→应用,k1=b@6; k3在第9步14>0被旧写复活成g@14。正确实现: k3第9步被墓碑挡住,第10步墓碑清除,最终k3不存在。
丙: k1墓碑在第7步被清除(18-8=10>=10);第8步cur=0→应用,k1=f@7。第8步G_before=18,G_before-R=8,Ver=7不满足Ver>8,不在不变量2前提内,故不违反。
   若到期写成G-t>R: 第7步10>10为假墓碑保留,第8步v7被忽略,墓碑延到第10步(30-8=22>10)才清,最终k1不存在(正确为f@7)。
不变量保证位置 / 钉住测试:
1 版本只进不退: store/store.go 的 applyOne 经 rule.Current 求 cur、rule.Applies 严格判 Ver>cur,忽略只增计数 — TestInvariantMonotonic
2 与朴素参照一致: store.purge 按 rule.Expired(G-t>=R) 清碑;仅 Ver<=G_before-R 的事件可致分歧 — TestNaiveReference
3 删除不被旧写复活: cur 优先取墓碑版本,Ver<=墓碑版本的 U 必忽略 — TestTombstoneBlocks
4 失败不留痕: store.Batch 先全量校验事件,再快照(行/墓碑/堆/G/ignored),超限即整体回滚 — TestRejectionLeavesNoTrace
