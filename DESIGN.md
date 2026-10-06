# 群聊频道服务：设计说明

实现位于 `channel/` 包（无第三方依赖，Go 1.26）。

- `channel/channel.go`：`Channel` 状态与构造、懒索引工厂。
- `channel/types.go`：配置、对外视图（`Message`/`Placeholder`/`Item`）与内部存储结构。
- `channel/errors.go`：按“拒绝次序”分类的错误。
- `channel/membership.go`：`Join`/`Leave`/`Promote` 与时钟校验。
- `channel/messages.go`：`Send`/`Edit`/`Recall`、正文/提及校验与索引增量维护。
- `channel/reads.go`：`MarkRead`/`Unread`/`UnreadMentions`/`Fetch`。
- `channel/fenwick.go`：可倍增容量的 Fenwick 前缀和树。
- `channel/doc.go`：包级文档（不变量与复杂度）。

## 1. 关键不变量

1. **序号无洞**：消息保存在只追加切片中，成功 `Send` 得到 `len+1`；撤回只打标记，不删除。
2. **时钟单调（相对被接受操作）**：`lastNow` 仅在校验通过、状态变更后置为本次 `now`；拒绝不推进时钟。
3. **读水位只增不减**（离开重进除外）：`MarkRead` 在锁内先判回退/越界，再单调赋值。
4. **可见性 = 序号 > 加入时水位**：`Fetch` 下界与未读区间都由 `watermark+1` 决定，重进时水位重置为当时最新。
5. **撤回占位**：撤回后仅保留序号、撤回者、撤回时刻；正文与提及清空。
6. **提及按当前集合判定**：`mentionBit` 随编辑对称差、撤回即时更新；是否计入未读只取决于“当前是否提及”且“序号 > 水位”。

## 2. 数据结构与复杂度

按序号建立三棵 Fenwick（前缀和树）：

| 索引 | 含义 | Send | Edit | Recall |
| --- | --- | --- | --- | --- |
| `aliveBit` | 未撤回消息数 | +1 | — | -1 |
| `authoredBit[u]` | u 作者的未撤回消息数 | 作者 +1 | — | 作者 -1 |
| `mentionBit[u]` | 未撤回且当前提及 u 的消息数 | 每个提及 +1 | 对称差 ±1 | 每个旧提及 -1 |

查询：

```
Unread(u)         = aliveBit.range(watermark+1, latest)
                    - authoredBit[u].range(watermark+1, latest)
UnreadMentions(u) = mentionBit[u].range(watermark+1, latest)
```

- `Unread` / `UnreadMentions`：2 次（或 1 次）Fenwick 区间查询，`O(log N)`，**与历史消息总数无线性关系**。
- `MarkRead`：常数次边界比较 + 一次水位赋值，**`O(1)`**（不触碰任何消息或计数）。
- `Send`：Fenwick 更新为 `O(log N)`，成员相关循环次数恰好等于提及人数（≤20），**与成员总数无关**；校验提及成员是 map 查找。
- `Edit`：与新旧提及集合大小之和线性（各自 ≤20），另加 `O(log N)` 索引更新。
- `Fetch`：`O(limit)`，与历史总数无关。

### Fenwick 的动态容量

Fenwick 节点聚合一个区间，简单“把数组翻倍并复制旧槽位”会让旧更新在新容量下丢失父链传播（实现过程中实测发现并修正）。当前实现额外保存原始值数组 `vals`，容量倍增后从 `vals` **整体重建**树。倍增使重建总成本为 `O(N)`（几何级数），故每次 `add` 摊销 `O(1)`，查询始终 `O(log N)`。

### 空间取舍

`mentionBit`/`authoredBit` 为每用户稀疏懒创建：只有“曾被提及/曾发言”的用户才有树。树容量为序号上限的下一个 2 次幂。在成员多、发言稀疏的场景，这是一种用索引换查询时间的取舍；如需进一步压缩，可改为每用户排序序号列表 + 双指针（见下）。

## 3. 被放弃的方案

- **每用户未读计数器（在 Send/Recall/Edit 时 fan-out 到所有成员）**：会让发送成本随成员数线性增长，直接违反“Send 只与提及人数有关”。放弃。
- **查询时线性扫描全部消息**：实现最简单（测试中的朴素模型就是如此），但 `Unread`/`MarkRead` 为 `O(N)`，无法满足规模要求。保留它作为差分测试的独立参考实现。
- **每用户有序序号集合（未读集合 / 提及集合）**：可做到集合大小级别的复杂度，且天然支持“删除提及/撤回”；但成员进出与“排除本人作者消息”需要额外集合做差，语义更新点分散。Fenwick 方案把所有计数统一为“区间和相减”，读路径最短、最易论证。作为后续可选优化保留。
- **读写锁 RWMux 分拆读路径**：本服务写操作频繁且读路径本就是 `O(log N)` 微秒级，单互斥锁已足够，且单锁使可线性化与重放确定性的论证最直接。放弃更细粒度锁以避免引入复杂的锁序。
- **消息 ID 与序号分离**：题目要求频道内连续序号且撤回占号不重排，直接以追加切片下标即序号，最简且无洞。

## 4. 并发与正确性论证

- 所有方法在同一互斥锁内完成“校验 + 状态变更 + 索引更新”。因此任一并发历史，按各操作临界区的某个全序排列即为等价串行执行（线性一致性）。
- 水位、序号、时钟、Fenwick 都只在锁内读写；`go test -race` 下并发混杂测试无告警。
- 确定性：所有逻辑只依赖入参与当前内存状态，无随机、无时间读取、无后台协程；相同操作序列在独立实例上重放，逐操作结果一致（`TestReplayDeterministic`）。

## 5. 拒绝次序的落地

每个方法内部严格短路：

```
参数(空标识/正文/提及/seq/limit 等)
  -> now 范围与回退
  -> 调用者成员身份
  -> 消息是否存在
  -> 权限(作者/管理员)
  -> 状态(已撤回 -> 超时 -> 次数超限；水位回退/越界)
```

两个易错点的明确处理：

- 编辑/撤回携带的新正文与提及集合属于**参数层**，即使消息已撤回也先报参数非法。
- 撤回的“权限不足”先于“已撤回”：无权调用者对已撤回消息仍报权限不足；作者本人或管理员重复撤回才报已撤回。

## 6. 测试

- 定向边界用例（`channel/channel_test.go`）：
  - 编辑时限差一秒/恰等于、次数上限 K；
  - 撤回时限差一秒/恰等于、管理员随时撤回他人消息；
  - 撤回后占位可见且保留撤回痕迹；已读后重新提及不计；编辑删去提及立即减；
  - 离开重进水位重置；水位回退/越界/恰等于无变化；
  - Fetch 的可见性、降序、`before` 分页、limit 边界、非成员拒绝；
  - 拒绝次序的每一对相邻类别；被拒操作不占号、不推进时钟。
- **差分测试**（`channel/naive_test.go` + `channel/diff_test.go`）：一份与生产代码完全独立、全量线性扫描的朴素模型；对 **1500 组**随机操作序列（每组 60 个操作、随机 E/R/K、成员进出/提升、各种非法输入与时钟回退）逐操作比对错误码、返回值与 Fetch 视图。`-v` 时打印每条输入、输出与判定依据（`agree` / `RESULT MISMATCH` / `FETCH MISMATCH: ...`），失败时打印完整序列日志与确定性种子以便复现。
- 并发与竞态（`channel/concurrency_test.go`）：8 发送者 ×200 的竞争发送，校验成功数恰等于最新序号且无洞无重号；读者持续查询/推进水位验证单调；`-race` 下通过。
- 重放确定性（同文件 `TestReplayDeterministic`）。

## 7. 本地验证方法

```bash
# 若 go 不在 PATH（本机实测装在 /usr/local/go）
export PATH=$PATH:/usr/local/go/bin
# 构建缓存若落在只读 HOME，可指向 /tmp
export GOCACHE=/tmp/gocache

gofmt -l .
go vet ./...
go test -count=1 ./...                         # 全量（含 1500 组差分）
go test -race -count=1 ./...                   # 竞态
go test -run TestRandomDifferential -v ./channel | head -40   # 查看判定日志

# 两档规模（10^4 与 10^6，相差两个数量级）性能对照
go test -run xxx -bench 'Benchmark(Unread|UnreadMentions|MarkRead|Send)' \
  -benchtime 20000x ./channel
```

本机（linux/arm64）实测（benchtime 20000x，历史中约 20% 提及、10% 撤回，水位停在中点）：

| 操作 | 10,000 条历史 | 1,000,000 条历史 |
| --- | --- | --- |
| `Unread` | ~47 ns/op | ~33 ns/op |
| `UnreadMentions` | ~25 ns/op | ~31 ns/op |
| `MarkRead`（等值无变化） | ~26 ns/op | ~21 ns/op |
| `Send`（1 个提及） | ~268 ns/op | ~173 ns/op |

历史扩大 100 倍，读路径耗时基本不变（波动在常数因子内，1M 档因预热/分支形态甚至更快），证明其不随历史线性增长；`Send` 在百万历史下仍是百纳秒级，且循环仅按提及人数进行。可将 `benchScale` 的 `n` 改为任意规模重复验证。
