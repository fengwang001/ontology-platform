# 设计：倒排索引短语查询与增量合并

## 1. 数据模型与约定
- 位置从 0 开始；每篇文档同一位置只有一个词元。
- 空串 "" 是合法词元；同一 (doc,pos,term) 重复添加被拒绝并计数 dupAdd。
- 倒排链：docID 升序；每文档内位置升序。增量编码（delta varint）：
  docID 存与上一 docID 的差；每文档存位置数及各位置相对首位置的差。

## 2. 短语匹配：多位置表同步推进（归并式）
查询 t0..tk-1，同 doc 取位置表 P0..Pk-1，游标 c0..ck-1：
- 锚点 a=P0[c0]，要求 Pi[ci]=a+i（i>=1）。
- 对每个 i：Pi[ci]<a+i 则 ci++；若 Pi[ci]>a+i，锚点失败，
  令 j=argmin(Pi[ci]-i)，把 c0 推进到 a=Pj[cj]-j 后重试。
- 命中后仅 c0++，其余游标保留。
重叠计数：命中后只推进锚点一位，"A A" 在 `A A A` 命中 (0,1)(1,2) 共 2；
“用过即跳”会错误得到 1。长度 1 退化为单词查询，每位置命中一次。
每次比较至少推进一个游标且只进不退 => 比较次数 O(sum len(Pi))，
断言上界 4*sum，绝非乘积。
若允许同位置多词元：位置表变为 (pos,tokenID) 并按 (pos,tokenID) 排序；
对齐条件为 pos 相差 i，同 pos 多候选按小簇做笛卡尔积，推进按整簇前移，
去重键含 tokenID，同位置不同词元算不同命中。

## 3. 布尔 AND：最短链驱动
最短链为主驱动 doc d；其余链单调游标前进到 >=d：相等则命中，
否则 d 跳到各链当前 doc 的最大值。比较次数 <= 4*最短链长*链数。

## 4. 段文件格式（自描述）
[0:8] magic "ONSEG001"；[8:12] dataLen(uint32 LE)。
data 区：dict = termNum(varint) 后接 termNum 条
  termLen(varint) term(utf8) postOff(varint) postLen(varint) chainCRC32(uint32)；
posts = 各词编码链顺序拼接，postOff 相对 posts 起点。
末尾 4 字节 = 对 magic+dataLen+data 的 CRC32-IEEE。
截断分类（实际长度 n）：
- n<12 头部不完整 ErrHeader；
- 12<=n<12+dictLen 词典不完整 ErrDict；
- posts 区被截断 ErrPosting；全长到达但 CRC 不符 ErrCRC。
dictLen=dataLen-postsLen，postsLen 由各词 postLen 求和；
但截断在 dict 时无法预知边界，恢复按顺序解析：某词 postOff+postLen
超出实际 posts 字节（含 CRC 可读前提）则剔除该词及其后；
仅保留链完整且 chainCRC 通过者 => 词典无悬挂指针。

## 5. 合并：一次多路归并
k 段每词维护链头，取最小 docID 归并；同 docID 多段链并为一文档，
位置归并去重；删除位图命中的 doc 整条丢弃。
每项读一次写一次 => 读项数==写项数==总项数（计数器断言）。
崩溃安全：先写 *.tmp，fsync 后原子 rename；启动清理 *.tmp。
合并中崩溃旧段不动可读，半截新段被识别删除。

## 6. 并发一致性
段集合为不可变快照 []*Reader 原子替换；查询持快照，合并完成后整体替换，
结果只来自某一快照，不新旧混合、文档不重复；删除位图随快照不可变。

## 7. verify
Open 校验 magic/长度/CRC，返回可 errors.Is 的 ErrHeader/ErrDict/ErrPosting/ErrCRC；
Recover 返回最大可恢复前缀；自检：docID 严格升、文档内位置严格升、
delta 可逆、dict 偏移/长度/chainCRC 与数据一致。

## 8. 边界语义
空索引可建可读（0 词）；单词单文档；查询词不存在=0；
短语长度超过文档长度=0。
