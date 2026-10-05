# 断线重连与补帧规划器 — 设计说明

## 结构
- `frames.Ring`：定长 K 环形缓冲，槽位存帧 size 与“到该槽位当前帧为止（含全部历史帧）”的累计和 cum。
- `snap.Store`：只留最新快照（帧号 s、大小 snapSize），帧号为 P 倍数的帧被追加时刷新。
- `resume.Planner`：一把 sync.Mutex 串行化全部操作；玩家表 + 帧环 + 快照。

## 取舍与证明
- 区间字节用 cum 前缀和 O(1)：sum[a..b] = cum(b)-cum(a-1)，空区间为 0；放弃逐帧累加（与区间长度成正比，且令 Reconnect 读取记录数随缺口增长）。
- 快照大小用 Ring 的最近 P 帧区间和取得后存档；快照帧被挤出时数值已固化，不再依赖缓冲。
- 增量判定：缺口在缓冲内（have+1 ≥ low）且（无快照 / have ≥ s / n1 ≤ cur-s+C）才走 Delta；放弃“缺口在缓冲内就一律增量”——陈旧客户端补帧代价可能超过“快照+尾差”，故用代价 C 比较，取等偏向增量。
- 证明 K ≥ P 时快照路径区间不早于 low：走快照必有 s>have（have≥s 会走增量），区间从 s+1 开始；s 是 ≤cur 的最新 P 倍数帧，cur-s < P ≤ K，故 s+1 ≥ cur-K+2，全部在缓冲内。
- 证明无快照时 have+1 ≥ low：无快照意味着 cur < P ≤ K，low = max(1, cur-K+1) = 1，have≥0，恒成立。
- 快照帧恰为 cur 时区间 [cur+1,cur] 为空，Bytes 仅 snapSize。

## 会话与拒绝次序
- 状态：online / disconnected(带断线时刻 t)；离场不存状态，用 now ≥ t+G 取等即时推导，离场者等同“不存在”。
- 错误次序：参数非法 > 时钟回退 > 玩家不存在 > 状态不符 > 令牌不符 > 超前；Join 先判在场再判名额。拒绝操作在任何修改前返回，不推进时钟。
- 时钟为“已接受操作最大 now”，由 Planner 统一维护；Ring 只存数据。
- Reconnect 是 ack 唯一可变小入口（置为 have）；Ack 回退静默成功不改字段。gen 仅在首次加入、离场后加入、重连成功时加 1；Disconnect 返回当前 gen 作令牌。

## touched 计数
- 非导出计数 touched：Append 与快照写入不计数；RangeSum 每个端点的存在性检查计一次，故一次 Reconnect 至多触及 2 条帧记录，与 K、缺口长度无关；快照大小读存档不计。frames_test 以 K=64 与 65536 对照。

## 本地验证
- `go test ./...`；`go test -race ./...`（含并发混合调用）；`go vet ./...`；`gofmt -l .`。
- resume_test 表驱动覆盖：代价取等、have 恰为 low-1 与再前一格、have≥s、s==cur、无快照、宽限取等、旧令牌、离场重入、ack 回退仅重连、六类拒绝次序及拒绝后状态不变。
- 另以 1500 组随机操作序列对照“保存全部帧、逐帧累加”的朴素模拟，日志打印每次操作的输入、输出与判定依据。
