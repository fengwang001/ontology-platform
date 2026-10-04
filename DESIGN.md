# 设计说明：流媒体离线下载许可管理器

1. 包划分：`device`（名额/冷却）、`license`（许可存储）、`playback`（exp/有效性/
原因纯函数）；根包 `offline` 的 Manager 持一把 `sync.Mutex` 串行化全部操作，天然
等价某串行顺序。子包不带锁，只做锁内领域逻辑；并发、参数校验、时钟单调、拒绝次序
统一在根包，避免双层锁与校验分歧。
2. 状态：accounts 集合；titles→titleEnd（实时取值，不入许可快照）；每账号 devices
集合、cooldowns[]{dev,until}；每账号许可 map key{dev,title}→{rentalEnd,firstPlay}。
注销即删该设备全部许可，重注册不恢复；许可只存在于已注册设备上。
3. 取舍一（开播后不看租期）：开播后 exp=min(firstPlay+Lp,titleEnd)。放弃
min(rentalEnd,playEnd,titleEnd) 三者最小方案：语义是“租期内开播即得完整播放期”，
故播放期可越过租期末尾；firstPlay 首次放行写入即不可变，Play 不续任何期限。
4. 取舍二（满即拒+冷却，不淘汰）：放弃 LRU 淘汰最久未用设备。占用=在册设备数+
未释放冷却名额；now>=注销时刻+Cool 恰等释放。同一设备自身冷却未释放前重注册走特例
复用该名额（不另占、不受满额限制），其余新设备满额一律拒绝，名额占用时点可复现。
5. 过期（恰等即过期，now<exp 才有效）：未开播 exp=min(rentalEnd,titleEnd)，已开播
exp=min(firstPlay+Lp,titleEnd)。原因取首个成立：已下架(now>=titleEnd)>播放期满
(已开播)>租期满(未开播)。titleEnd 实时取：延后可救活仅因下架过期者；因租期/播放期
满过期的记录判定不依赖 titleEnd，不复活。
6. Download：同键有效时未开播仅改 rentalEnd 续期（不占名额），已开播拒续；已过期按
新签发替换；仅新签发检查“此刻有效许可数<Omax”，下架检查排在 Omax 之前。
7. 时钟/原子性：lastNow 记已接受操作最大 now，回退即拒；被拒不改任何状态（含
lastNow、firstPlay），全部校验在写状态前完成。
8. 复杂度与 touched：计数随自然遍历累加（+1 次 map 定位）：Register 只遍历本账号
冷却（≤未释放名额+1）；Download 只遍历本账号许可（≤本账号记录数+1），与他账号
无关；Play/Status map 直定位触碰 1。用他账号 100/10000 条两档对照断言。
9. 本地验证：`go test -race ./...`、`go vet ./...`、`gofmt -l .`；表驱动边界用例、
touched 两档对照、并发不变量，另用 1500 组随机序列与“从头重放全量重算”的朴素模型
逐步对照，-v 日志打印每步输入、输出与判定依据。
