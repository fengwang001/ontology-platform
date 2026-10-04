# 保留租约历史保留与副本追赶规划器 — 设计说明

## 结构
- `history`：操作历史。记录为 (seq, id, kind) 序列；`latest[id]` 记同 id 最大 seq（O(1) 判取代，禁止扫全史）；`H` 完整下界。
- `lease`：管理器。租约集合 + `gcp`，Add/Renew/Remove/SetGCP，过期判定 `now-lastRenew > E`；不主动剔除。
- `recovery`：只读 `Plan(c)`，经 `Source` 接口取 maxSeq/H/ops/live，不依赖 history 包（避免环依赖）。
- 协调器 `history.Primary`：单互斥锁串行化全部操作（满足"等价某串行顺序"），持有时钟、统一强制参数/时钟/拒绝次序，再调用 history 与 lease。

## 取舍
- 过期不等于失效：过期只在 `Merge` 开头统一剔除；此前可 Renew 续活、仍占 Lmax 名额。放弃"到期即惰性失效"，因为题目要求过期未剔除可续活且被拒不剔除。
- Merge 只清区间 `[H,floor)` 内被取代者或 Delete；未被取代的 Index（存活文档）保留，但 `H` 仍推进——保留项是冗余安全垫，不影响 seq≥H 无缺失的语义。放弃"下界之下全删"，否则存活文档也会丢、FileBased 无源。
- floor = min(gcp+1, 现存租约最小 r)；保证 H ≤ gcp+1 且租约 r ≥ H。
- 拒绝次序由 Primary 单点排序：参数非法 > 时钟回退 > 租约存在性 > 租约回退 > 历史不可得(r<H) > 超限；Delete 文档不存在、gcp 回退排在时钟之后。被拒操作不触碰任何状态。
- 取代判定用 `latest` map：O(1)。`touched` 计数器非导出，Merge 前清零，仅对区间内记录自增，故 ≤ floor-旧H，与 maxSeq/存活数无关。
- OpsBased 升序返回 seq>c 全部操作（c+1≥H 保证连续）；FileBased 返回存活文档（id 字节序，带最新 Index seq）与 maxSeq。

## 放弃的方案
- 每包各自锁/两阶段提交：拒绝次序跨 history/lease，分散加锁易错；单锁等价串行且顺序确定。
- Recovery 直接 import history：会与 history 使用 recovery 的类型形成环，改用窄接口。
- 惰性清除（读时过滤）：无法精确复现 Merge 清除条数与 H 推进，且 touched 上界无法证明。

## 本地验证
- `go test ./...`：表驱动边界用例 + 题目两个示例 + 1500 组随机序列。
- 随机测试含逐步朴素模型（独立重写规则），逐步比对所有返回/错误/剔除；每步对每个 c 应用 Plan(c) 校验副本终态==主存活集；日志打印输入、输出、判定依据（`-v`）。
- touched 对照：历史 1000 与 100000 条、清除区间同 10 条，断言 touched 都=10。
- `go test -race ./...` 与 `go vet ./...`。
