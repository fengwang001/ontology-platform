# 火山模型算子流水线 — 设计推导

每个算子实现 `Open/Next/Close`，执行器从根反复 `Next` 拉取，`Next` 返回
`io.EOF` 表示耗尽。

## 1. Close 责任与顺序

树中每个节点有且仅有一个父节点。规则：**父在自己的 Close 里 Close 子节点，
除此之外没有任何地方 Close 别人**。因此 Close 边与 Open 边一一对应，
自上而下（根→叶）传播，每个算子恰好一次。驱动方（`op.Run`）无论因耗尽、
LIMIT 满足还是出错而停止，都只调用一次 `root.Close()`。

每个 Close 用 `sync.Once` 保护，重复调用幂等返回 nil，不重复释放资源。
不变式：**凡 Open 成功返回的算子，必被 Close 恰好一次**。Open 阶段失败也要
回滚：父先 Open 子，子成功而父自身初始化失败（或更深层失败）时，立即
Close 已打开的子再返回错误。

## 2. 异常路径

Next 报错逐层冒泡；`Run` 用 defer 保证 `root.Close()` 必然执行，Close 沿树
传播，故正常、Next 报错、Close 报错三条路径上已 Open 的节点全部被关。

## 3. Close 报错聚合

Close 遇到错误不能中断：父按固定顺序 Close 全部子节点，用 `errors.Join`
聚合。所有节点无论前面是否报错都会被 Close 到；三个算子同时报错时三条
错误都能经 `errors.Is`/`errors.As` 取到。重复 Close 返回 nil，不污染聚合。

## 4. LIMIT 下推

- 无排序：LIMIT k 吐够 k 行后置位 done，此后 `Next` 直接 EOF 且不再调用
  下层，故扫描读取恰为 k 次（约束保证 ≤ k+1）；随后 Close 立即回收。
- 有排序：排序必须看完全部输入才能确定全局顺序（即使 top-k 也必须看到
  所有元素），LIMIT 不能穿过排序下推。扫描读取数=总行数，排序只向上吐
  k 行，k 行后被 Close 中止。
- LIMIT 0：选择**不 Open 下层**——Open 直接标记完成，Next 立即 EOF，
  Close 无操作；下层零资源零读取。

## 5. 排序与溢出

内存上限 M 行：驻留达到 M 行时，将内存排序为一个有序 run 整体写入溢出
文件并清空，峰值恰为 M。溢出文件（小端）：
`magic(4)|runCount(4)`；每 run `rowCount(4)`；每行
`len(4)|payload|crc32(4)`，crc 覆盖 len 前缀与 payload。Close 删除全部
溢出文件。

截断分类（从第 1 字节截到 size-1，偏移 b 为保留长度）：
`b<8` 头部不完整；落在某行 len 的 4 字节窗口→行长度前缀不完整；len 完整
但其后 payload+crc 不足→行体不完整；结构完整但校验失败→CRC 不匹配。
四类为包级哨兵错误，`errors.Is` 可判；截断时中止，绝不返回少行结果。

## 6. 并发

单树不要求并发安全；`res.Registry` 用 mutex 保护计数，多树多 goroutine
共享记账安全，`-race` 干净。额外保证 Close 与在途 Next 并发不 panic：
close(stop) 后 Next 检测到关闭返回哨兵 `ErrClosed`，Close 等待在途 Next
退出（WaitGroup）再释放资源。

## 7. 稳定性与边界

键相同按输入序号决胜（稳定排序）。零行、LIMIT 0、LIMIT>总行数、过滤全
不满足、单节点树均自然成立。

包：`op`（接口/Row/Run/哨兵）、`res`（记账与泄漏检测）、`leaf`（扫描）、
`unary`（过滤/投影/限量）、`sorter`（上限/溢出/归并）、`cmd/demo`。
