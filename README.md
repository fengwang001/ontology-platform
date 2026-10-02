# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## TCP 已建立连接段校验器（RFC 5961 风格）

`rfc5961` 包对到达已建立连接的 TCP 段做窗口与标志合法性判定，所有序号按模 2^32、以相对偏移 `(x-基准) mod 2^32` 比较。入口为 `rfc5961.New(rfc5961.Config{...})` 与 `(*Validator).Process(now, seg)`，返回动作 `Drop` / `AckPlain` / `AckChallenge` / `Suppressed` / `Reset` / `Accepted`。

### 检查次序（Process 固定流程）

1. **RST 三分判定**：RST 一律按 L=0 判可接受性（忽略 Len/FIN）。窗口外 → `Drop`（静默丢弃）；`Seq == rcvNxt` → `Reset`，连接变 Closed；在窗口内但不等于 rcvNxt → 走挑战 ACK（fromRST=true）。
2. **非 RST 段窗口检查**：`[Seq, Seq+L)` 与接收窗口不相交 → `AckPlain`（普通应答，不碰限速器、不改序号）。rcvWnd=0 时仅允许 L=0 且 Seq==rcvNxt；L>0 时首字节或末字节在窗口内即可接受；偏移 ≥ 2^31 表示落在 rcvNxt 左侧。
3. **SYN**：可接受的 SYN → 挑战 ACK（fromRST=false）。
4. **缺 ACK**：可接受的非 SYN 段若没有 ACK 标志 → `Drop`。
5. **ACK 号范围**：必须落在闭区间 `[sndUna-maxSndWnd, sndNxt]` 内，判据为 `(Ack-lo) mod 2^32 <= (sndNxt-lo) mod 2^32`；越界 → 挑战 ACK。
6. **通过后 Accepted**：Ack 位于 `(sndUna, sndNxt]` 时推进 sndUna；`maxSndWnd=max(maxSndWnd, Wnd)`；段起点在 rcvNxt 或其左侧且终点严格越过 rcvNxt 时推进 rcvNxt（左侧重叠被裁掉，窗口内乱序不推进）。除 Reset 外只有 Accepted 改序号。

### RST 三分判定的理由

窗口外的 RST 与本连接无关，静默丢弃可避免盲注入造成的 off-path 副作用；恰好命中 rcvNxt 的 RST 满足精确序列匹配才允许拆除连接；仅“在窗口内”不够精确，此时只回挑战 ACK 要求对端用可验证序号重传。

### ACK 范围为何两端闭合

下界 `sndUna-maxSndWnd` 含端点：滞后不超过对端曾通告最大窗口的 ACK 可能是合法的旧反馈重传，应当处理而非挑战。上界 `sndNxt` 含端点：sndNxt 是“下一个待发序号”，确认到 sndNxt 恰好覆盖全部已发数据，是合法的累积确认值。

### 挑战 ACK 限速

限速器状态为窗口起点 `ws`、窗口内挑战总数 `cnt` 与其中 RST 触发数 `cntR`，只在真正需要发挑战时推进：窗口为空或 `now-ws >= P` 时以 now 开新窗并清零；`cnt < C` 且（非 RST，或 `cntR < ceil(C/2)`）才发送并计数，否则 `Suppressed`。因此每窗口挑战总数不超过 C，RST 触发不超过 ceil(C/2)，其余配额留给 SYN 与 ACK 范围挑战；被抑制的挑战不改变 `ws/cnt/cntR`。Drop 与 AckPlain 完全不使用限速器。

### 拒绝原因次序

参数非法（now 或段字段越界、SYN+RST、构造参数越界）→ 时钟回退（now 小于上次被接受调用）→ 连接已 Closed，按此顺序只报第一个；被拒绝调用不改变任何状态（含时钟与限速器）。六种动作都是被接受的调用，均推进时钟。

### 本地验证

```bash
# 全部单测（含题目给定边界例、限速例、拒绝次序、重放确定性）
go test -race -v ./rfc5961

# 2000 组随机段序列与朴素逐步模型对照，日志含输入、输出与判定依据
go test -run TestNaiveModelDifferential -v ./rfc5961

go vet ./... && gofmt -l .
```

说明：若 `$HOME/.cache` 只读，可设置 `GOCACHE=/tmp/go-cache` 后再运行 go 命令。
