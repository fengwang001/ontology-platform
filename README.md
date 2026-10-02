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

## trustanchor：RFC 5011 风格信任锚跟踪器

`trustanchor` 包从初始信任锚出发，按已信任密钥签名的密钥集观测自动更新信任锚，
各密钥的状态、计时起点与观测判定可精确复现。

### 构造参数

| 参数 | 含义 | 取值 |
| --- | --- | --- |
| `AddHold` (H) | 加入保持期 | 0..1e9 秒 |
| `Retain` (R) | 吊销与缺失保留期 | 1..1e9 秒 |
| `MaxTracked` (Kmax) | 跟踪密钥数上限 | 1..1000 |
| `MinAppear` (M) | 晋升所需连续出现次数 | 1..100 |
| `Quorum` (Q) | 签名法定数 | 1..Kmax |
| `Anchors` | 初始信任锚（均 VALID） | Q..Kmax 个互不相同的非空标识 |

任一参数非法则 `NewTracker` 以 `ErrInvalidConfig` 整体拒绝。

### 状态机

每个密钥处于 `ADDPEND`（带 `since` 与连续出现次数 `cnt`）、`VALID`、`MISSING`
（带 `since`）、`REVOKED`（带 `at`）之一；`BANNED` 为永久封禁集合（不占 Kmax）；
其余为未跟踪。`State(id)` 返回当前状态与计时起点。

### Observe 判定次序（只报第一个）

1. `ErrInvalidArgument`：密钥集 1..64 项、标识非空且互不相同、签名者标识非空、now ∈ [0, 1e15]。
2. `ErrClockRollback`：now 小于此前被接受观测的最大 now。
3. `ErrUntrustedSigners`：签名者去重后，观测开始前状态为 VALID 的个数小于 Q
   （MISSING/ADDPEND/REVOKED/未知均不算，重复与未知签名者不凑数）。
4. `ErrLockup`：三步处理后 VALID 个数小于 Q（含当次晋升，故 VALID 永不低于 Q）。
5. `ErrOverflow`：处理后被跟踪（不含 BANNED）密钥数超过 Kmax。

通过前两关后在副本上执行三步处理，全部完成并通过检查才整体提交；
任何拒绝都不改变状态、计时起点、BANNED 集合与时钟。

### 三步处理（同一 now）

1. 被跟踪而不在密钥集中的密钥：`VALID→MISSING(since=now)`，`ADDPEND→未跟踪`
   （计时丢失），`MISSING`/`REVOKED` 不变。
2. 密钥集逐项：`BANNED` 标识整项忽略；吊销位为真时 `VALID/MISSING→REVOKED(at=now)`、
   `ADDPEND→未跟踪`、`REVOKED` 不刷新 at、未跟踪者忽略；吊销位为假时
   `未跟踪→ADDPEND(since=now, cnt=1)`、`ADDPEND` 的 `cnt` 加一（since 不刷新）、
   `VALID` 不变、`MISSING→VALID`（since 清除）、`REVOKED` 不变。
3. `ADDPEND` 且 `now >= since+H` 且 `cnt >= M` 者晋升 `VALID`（H=0 且 M=1 时当次晋升）；
   `REVOKED` 且 `now >= at+R` 者移入 `BANNED`；`MISSING` 且 `now >= since+R` 者变未跟踪。

签名者按观测开始前状态判定，因此密钥可自签吊销；锁死按处理后状态判定，包含当次晋升。

### 并发与可复现性

所有方法经互斥锁串行化，并发调用等价于某个串行顺序；相同观测序列重放得到完全
相同的状态与错误。内部以非导出计数器记录每次 Observe 处理的密钥项数，不超过
密钥集项数加被跟踪（不含 BANNED）密钥数。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照，-v 打印输入/输出/判定依据）
go test ./trustanchor/
go test -race -v ./trustanchor/

# 单个用例
go test -run TestSpecExampleWalkthrough ./trustanchor/
```
