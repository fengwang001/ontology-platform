# 带决定缓存与保留配额的尾部采样器：设计说明

## 取舍
- 包划分：`span` 只描述单个 trace 的缓冲状态（lastSeen、跨度数、spanID 集合、错误、最大 durMs）；
  `policy` 只做纯策略判定（Error → Latency → Prob/Sampled out），无时钟无 IO；
  `sampler` 组合两者并管理缓冲、配额、缓存、并发，职责单一、可单测。
- 缓冲用 `map[string]*bufferNode` + 按 `(lastSeen, traceID)` 排序的最小堆。
  追加跨度更新 lastSeen 时 `heap.Fix`，静默 Tick 与满驱逐都从堆顶取，O(log N)，不做线性扫描。
- 决定缓存作为 sampler 包内非导出模块（`cache.go`）：map + 按 `(decidedAt, traceID)` 的淘汰堆；
  读取即判到期，写入前先清全部到期项，仍超容量再淘汰最旧（并列 traceID 小者）。
  Td=0 或 Cmax=0 时不构造缓存（命中永远为否），迟到跨度因而作为新 trace 进入缓冲。
- 配额只记 `(窗口号, 已用量)`；窗口号变化即清零，无需后台。Error/Latency 恒保留并照常占用
  （u 可超过 Q），Prob 候选受 `u < Q` 约束，失败给 "Budget"。
- 并发用一把互斥锁串行化 Ingest/Tick，天然满足“等价于某个串行顺序”；纯策略与哈希在锁外可测，
  但调用点都在临界区内，结果确定。
- 决定清单按产生顺序 append：驱逐决定先于新 trace 的 Sc 决定；Tick 按堆顺序逐个决定。

## 被放弃的方案
- 缓冲满时拒收新跨度：会把背压转嫁给调用方且破坏“每个被接受跨度恰入四个出口之一”的账目；
  改为立即对最旧 trace 做降级决定（Evicted），数据不丢、决定仍可复现。
- Error/Latency 不占配额：会让必保留类无限挤出 Prob；现令其占用但不受配额约束（u 可超 Q），
  既兑现必保留，又让 Prob 在同一窗口内真实受预算限制。
- 缓存到期后无限期记住 trace 身份：会让迟到碎片永远并入旧决定；改为到期即视为不存在，
  迟到跨度开启新 trace（旧跨度数不合并），语义明确且缓存有界。
- Tick 全量扫描缓冲：放弃，改为只从堆顶弹出 lastSeen+W ≤ now 者，考察项数 ≤ 决定数+1。

## 本地验证
- `go build ./...`
- `go test -race -v ./span ./policy ./internal/cache ./sampler`
- `gofmt -l . && go vet ./...`
