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

## 推送设备令牌注册表（`ontology` 包）

`ontology/registry.go` 提供并发安全的推送设备令牌注册表 `Registry`。

### 构造与参数

`NewRegistry(D, R int, G, B int64) (*Registry, error)`

- `D`：每用户设备上限，范围 `[1, 64]`。
- `R`：每个绑定保留的旧令牌数，范围 `[0, 8]`（0 表示旧令牌立即释放）。
- `G`：旧令牌下发宽限时长，范围 `[0, 10^9]`。
- `B`：服务商失效反馈后的拒绝窗口时长，范围 `[0, 10^9]`。

用户/设备为非空、不超过 64 字节的字节串；令牌为非空、不超过 256 字节的字节串；`now` 为 `[0, 10^12]` 的整数，且不得小于已接受变更的最大时刻。

### Register 的释放与淘汰次序

`Register(u, d, tok, now)` 先校验拒绝窗口（`blk[tok] > now` 拒绝），然后严格按以下次序执行：

1. **释放既有归属**：
   - `tok` 已是 `(u,d)` 的当前令牌：仅刷新 `lastSeen=now` 后结束；
   - `tok` 是其他绑定的当前令牌：删除该绑定整体（连同其旧令牌）；
   - `tok` 是任一绑定（含自己）的旧令牌：只从旧令牌列表移除。
2. **绑定已存在**：原当前令牌以 `(原令牌, now)` 插入旧令牌列表头部，超过 `R` 项的最旧项被释放，再设置新的当前令牌与 `lastSeen`。
3. **新设备**：设备数在步骤 1 删除之后再判定。达到 `D` 时淘汰该用户 `lastSeen` 最小（并列取设备名字节序小者）的设备并释放其全部令牌；因此把同用户另一设备的当前令牌转到新设备时，设备数先减，不会触发淘汰。淘汰释放的令牌立即可被注册。

### 旧令牌宽限

`Targets(u, now)` 先按 `lastSeen` 降序、设备名字节序升序列出各当前令牌，再追加满足严格不等式 `now < 退役时刻 + G` 的旧令牌（按退役时刻降序、令牌字节序升序）。宽限边界：`退役+G-1` 仍有效，`退役+G` 已过期。过期旧令牌不会被自动删除，仍占用 `R` 的名额、仍保持唯一归属，也仍可被他人注册夺走。

### 失效反馈与拒绝

- `Feedback(tok, now)`：令牌无归属返回 `ErrTokenUnowned`；当前令牌则删除整个绑定，旧令牌则只移除；随后置 `blk[tok] = now + B`。`B=0` 时拒绝阈值等于 `now`，同一时刻即可重新注册；窗口判定为严格 `blk[tok] > now`，到达 `blk` 时刻即放行。
- `Unregister` 释放的令牌不进入拒绝表。

### 拒绝原因优先级（只报第一个）

1. 参数非法：`ErrInvalidArgument`（构造越界、用户/设备/令牌为空或超长、`now` 越界）；
2. 时钟回退：`ErrClockSkew`；
3. 操作特有：Register 为 `ErrTokenBlocked`；Touch/Unregister 为 `ErrDeviceNotFound`；Feedback 为 `ErrTokenUnowned`。

被拒绝的操作不改变任何绑定、令牌归属、拒绝表与最大时刻。

### 并发与索引

所有操作用单个 `sync.RWMutex` 串行化变更（`Targets` 为读锁），结果等价于某一串行顺序。绑定按用户建有非导出索引 `devices map[user]map[device]*binding`，其长度即每用户设备计数器（测试 `assertInvariants` 会校验它与实际绑定数一致），因此 `Targets` 只考察该用户的设备与旧令牌，开销不随其他用户数据增长。令牌归属索引 `owners` 保证任一时刻每个令牌至多归属一个绑定。

### 本地验证

```bash
# 全部测试（含 -race）
go test -race ./...

# 规则用例详细输出
go test -race -v ./ontology

# 2000 组随机序列与朴素模型对拍，并打印每组操作的输入、输出与判定依据
go test ./ontology -run TestDifferentialFuzz -log_fuzz -v

# 代码检查
gofmt -l . && go vet ./...
```
