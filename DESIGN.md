# 日志限频采样器设计说明

## 结构
- `window`：条目 `(win, cnt, dropped)` 与放行判定。`cur=floor(now/W)`；条目 win 过期先出
  Rolled 摘要再重置；sev<4 时 `cnt++`，`cnt<=N` 或 `(cnt-N)%M==0` 放行，否则 `dropped++`；
  sev>=4 一律放行且不计数。
- `keytable`：每租户一个 LRU 键表（`map[key]*list.Element` + 双向链表），容量 Kt，
  链表头为最久未用。任何 Record（无论放行、无论 sev）都把该键移到链表尾。
  另维护非导出计数器 `examined`，记录每次操作考察的条目数，供测试断言 O(1)。
- `report`：`Summary{Tenant,Key,Window,Dropped,Reason}`、`Reason{Rolled,Evicted,Closed}`、
  `Sink` 类型，纯数据定义，不含逻辑。
- 根包 `sampler`：参数校验、时钟（maxNow）、并发互斥（一把 `sync.Mutex`，所有操作
  等价于某串行序）、把 window/keytable/report 串成 Record/Pending/Flush。

## 关键取舍
1. 键表满时**淘汰最久未用键**，而非拒收新键、也非合并成溢出桶。
   - 拒收会让突发新键全部丢失且无法采样；溢出桶会让"每指纹"语义失真（桶内多键
     共享 cnt，放行/丢弃无法归因到具体键，摘要的 Key 字段无从填写）。淘汰代价是
     冷键额度重置，可接受。
2. 淘汰后**额度重置**，不保留墓碑。
   - 墓碑要占内存且需老化策略，复杂度换来的只是"少放行几条"的严格性；题目明确
     接受略多放行。淘汰时若 dropped>0 先出 Evicted 摘要，丢弃数不丢。
3. 摘要**随下一次 Record 或 Flush 交付**，不用定时器。
   - 定时器引入后台 goroutine，破坏"相同输入重放得到相同结果"的确定性，也让
     并发语义（等价串行序）难以陈述。惰性交付下，不变量
     `已接受 = 已放行 + 摘要Dropped之和 + 当前pending dropped` 在任意时刻成立，
     既不丢也不重复计：重置/淘汰只在 dropped 交付后才清零，Flush 仅 sink 成功才清零。
4. 淘汰/重置**互斥**：Record 命中已存在的键只会 Rolled；未命中才可能 Evicted，
   而新建条目 win=cur、dropped=0，不会再触发 Rolled，故每次 Record 至多一条摘要。
5. Flush 在**持锁状态**下同步调 sink，按 (租户,键) 字节序快照后逐项交付；
   sink 失败即停，失败项与后续项 dropped 原样保留，可重试。

## 本地验证
```
export PATH=$PATH:/usr/local/go/bin
go vet ./... && go test ./... -v
```
测试覆盖：N=0、M=1、(cnt-N) 恰为 M 倍数与差 1、窗口边界恰等 W、sev=4 不耗额度、
按访问序淘汰、Evicted/Rolled 互斥、租户隔离、sink 第 j 项失败后保留与重试、
被拒操作不改状态、Kt=100 与 10000 下 examined 计数同为常数、随机序列对照朴素模拟。
