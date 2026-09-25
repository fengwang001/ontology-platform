# Aho-Corasick 推导与不变量

## 三、模式 {"a","ab","bab"}（编号 0,1,2），文本 "abab"

trie 节点（root 出发的路径，* 为终态）：root；a*(0)；ab*(1)；b；ba；bab*(2)

fail 链：fail(root)=root，fail(a)=root，fail(b)=root，fail(ab)=b，fail(ba)=a，fail(bab)=ab
输出链（本节点终态 + 沿 fail 的终态祖先）：out(a)=[a]，out(ab)=[ab]，out(ba)=[a]，out(bab)=[bab,ab]

| pos | 字符 | 落定节点（含 fail 跳转） | 本步报告 (模式,End) |
|-----|------|--------------------------|---------------------|
| 1 | a | root→a | (a,1) |
| 2 | b | a→ab | (ab,2) |
| 3 | a | ab 无 a 孩子→fail 到 b→ba | (a,3)（ba 非终态，沿 fail 到终态 a）|
| 4 | b | ba→bab | (bab,4)、(ab,4)（先本节点，再 fail 链上的 ab）|

共 5 个匹配：(a,1) (ab,2) (a,3) (bab,4) (ab,4)。

(甲) 失配直接回根重扫：pos3 从根走到 a 报 (a,3)，pos4 从 a 走到 ab 报 (ab,4)；漏掉 (bab,4)，共 4 个。
(乙) 只报落定节点本身、不沿 fail 链：漏 (a,3)（落定 ba 非终态）与 (ab,4)（ab 在 bab 的 fail 链上），共 3 个。
(丙) End 误记为末字符下标（少 1）："bab"（起点1，占[1,4)）错报 3，正确 4；"a"（起点0，占[0,1)）错报 0，正确 1。

## 二、四条不变量的保证位置与钉住测试

1. 与朴素参照一致：api.naive 逐起点逐字符比较作参照；测试 TestMatchAgainstNaive（多重集逐 (模式,End) 相同）。
2. 覆盖全部出现恰好一次：trie.Build 把输出链展平进 out、ac.Match 每落定节点全报 out；测试 TestMatchAgainstNaive。
3. 位置自洽：ac.Match 以 End=i+1（左闭右开）报告；测试 TestPositionSelfConsistent 逐条切 text[End-len:End] 核验。
4. 失败不留痕：api.build 先全部校验、cur 仅在成功时赋值，Match 先查未构建再验文本；测试 TestStateUnchangedAfterRejection。
