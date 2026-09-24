# 采样剖析与热点归因器 设计推导

## 数据模型
- 栈：帧名序列，统一为根→叶；空栈拒绝。
- 树节点 Node{Frame, Depth, Truncated, Self, Total, Children}，虚拟根 depth=0。

## self / total
- 每条样本沿路径匹配，末端节点 Self+1，路径上每个节点 Total+1。
- 恒等式：所有真实节点 Self 之和 == 样本总数（每条样本恰落在一个末端）。
- Total 之和 >= 样本总数；栈深 >1 时严格大于（祖先被重复计入）。

## 递归帧的函数级归因
- 同一函数 F 可在一条栈中出现多次（A→F→G→F→H）。
- 直接把所有 F 节点 Total 相加：祖先 F 已包含后代 F 子树，重复计数。
- 规则：对每条根→叶路径，只计「不被另一个 F 包住」的最外层 F；
  DFS 用「当前路径是否已有 F」标记，遇到内层 F 时整棵子树跳过。
- 故 A→F→G→F→H 中 F 的函数级 total == 最外层 F 的 total == 1，而非 2。

## 深度截断
- 深度恰好等于上限不截断；上限+1 丢弃更深处帧。
- 保留的最末帧置 Truncated 标记，TruncatedSamples 记录被截断样本数；不丢整条样本。

## 复杂度
- Add 只做 depth 次子查找；计数器 cmps 记录比较次数，
  depth 20 的 10 万栈总比较 <= 10万*20*4。
- 归因：一次遍历 + 一次 sort，不重建树（rebuildCount 恒 0）。

## 采样
- 注入时钟（下一 tick 时间）与栈来源（返回栈或 busy 丢样）。
- 时钟回拨/间隔异常：跳过该 tick，abnormal++。
- 丢样 dropped++；self 之和 + dropped == 期望采样数。
- Stop 幂等；一把 RWMutex，查询只看快照，看不到半更新树。

## 落盘格式
- 头："PROF1" + version(1B) + count(4B BE)，共 10B。
- 节点记录（先序 DFS）：depth(1)+trunc(1)+namelen(2 BE)+name，记录末 0x00。
- 记录序列后 CRC32-IEEE(全部记录字节)(4 BE)。
- 截断分类：头不足 ErrShortHeader；记录/终止符不足 ErrShortRecord；
  记录完整但 CRC 不足/不符 ErrCRC；均支持 errors.Is。
- 先序回放：子节点 depth<=父节点 depth 时先闭合，不出现无父之子。

## 边界
- 零样本归因返回空切片、无错误；空栈拒绝并计 Invalid。
