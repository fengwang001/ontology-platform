# merge-on-read KV：推导与不变量

## T=4 七步（base 初值为空）

| 步 | 操作 | delta | base | 自动合并 |
|---|---|---|---|---|
| 1 | Set a=1 | [a:1] | {} | 否(1<4) |
| 2 | Set b=2 | [a:1,b:2] | {} | 否 |
| 3 | Set c=3 | [a:1,b:2,c:3] | {} | 否 |
| 4 | Del c | [] | {a:1,b:2} | 是(4>=4)，c 被移除 |
| 5 | Set b=9 | [b:9] | {a:1,b:2} | 否 |
| 6 | Set c=7 | [b:9,c:7] | {a:1,b:2} | 否 |
| 7 | Set c=8 | [b:9,c:7,c:8] | {a:1,b:2} | 否(3<4) |

- (甲) 第4步后 base={a:1,b:2}；delta 空，Read(c)→("",false)。若折叠成保留 base["c"]=""，会错成 ("",true)：空值但「存在」。
- (乙) 第7步后 Read(c) 扫描 3 条 delta（b:9 跳过、c:7、c:8），→("8",true)，合并代价 3。若「旧到新命中第一条即停」会错成 "7"。
- (丙) 代价 3=扫描时 len(delta)≤T-1，与历史总数无关；不 compact 则 delta 长 7、代价 7 且随历史线性增长，违反不变量 3 与 Read 为 O(T) 的可证明复杂度。

## 四条不变量：保证位置 / 钉住的测试

1. 与朴素重算一致：store.go 的 Read（取 base 后顺序套用全部 delta）与 compact（同序折叠）；TestNaiveRecompute。
2. 删除墓碑语义：merge.go Apply 遇 Del 置 Ok=false；store.go compact 用 delete 移除键；TestTombstoneSemantics。
3. 合并代价有界：store.go Set/Del 追加后 len(delta)>=T 立即折叠清空；Read 计数器=len(delta)；TestMergeCostBounded。
4. 失败不留痕：store.go New 先验 T、Set/Del 在改任何状态前先拒空键，哨兵错误不同；TestRejectLeavesNoTrace。
