# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 增强二次机会时钟置换器

`enhancedclock` 包实现带写回计数的增强二次机会时钟置换器。`New(n)` 创建 `n` 个帧，帧号为 `0` 到 `n-1`，扫描指针初始为 `0`。

### 访问规则

- `Access(page, write)` 访问页时，命中会把引用位置为 `1`；`write=true` 时同时把脏位置为 `1`。
- 缺页且存在空帧时，选择编号最小的空帧，新页引用位为 `1`、脏位等于本次是否为写，扫描指针保持不动。
- 缺页且没有空帧时，从当前指针所指帧开始顺时针扫描并选择牺牲页。
- 装入牺牲帧后，新页引用位为 `1`、脏位等于本次是否为写，指针移动到牺牲帧的下一帧；从 `n-1` 驱逐后回绕到 `0`。

### 两趟扫描

每次扫描都从当前指针开始，恰好走一圈：

1. **甲趟**：选择第一个“引用位 `0`、脏位 `0`”的帧；这一趟不修改任何位。
2. **乙趟**：按顺序检查帧。遇到引用位 `1` 的帧，先把引用位清为 `0` 并继续；遇到选择前的第一个“引用位 `0`、脏位 `1`”帧即选中。
3. 如果甲趟、乙趟后仍需继续，则从原指针重新执行下一组甲趟、乙趟。乙趟清掉的引用位会立即影响后续甲趟。

只有脏牺牲页被驱逐时增加一次写回。`Flush(page)` 可显式刷写驻留脏页：成功时增加一次写回、清掉脏位，但不改变引用位和扫描指针。

### 错误与并发

- `New(0)` 或更小值返回 `ErrInvalidFrameCount`。
- 页号为负时，`Access` 和 `Flush` 返回 `ErrNegativePage`，该校验优先于驻留状态。
- 刷写不驻留的页返回 `ErrPageNotResident`；刷写驻留但不脏的页返回 `ErrPageNotDirty`。
- 被拒绝的操作不会修改帧、扫描指针或写回计数。
- `Access`、`Flush`、`Pointer`、`WriteBacks`、`Frames` 和 `Snapshot` 通过互斥保护；`Snapshot` 一次返回同一串行时刻的帧、指针和写回计数副本。
- 任何时刻驻留页互不相同，驻留数量不超过帧数；总写回数等于脏页驱逐次数与成功刷写次数之和。

### 使用示例

```go
replacer, err := enhancedclock.New(4)
if err != nil {
    return err
}

result, err := replacer.Access(42, true) // true 表示写访问
if err != nil {
    return err
}

if err := replacer.Flush(42); err != nil {
    return err
}

state := replacer.Snapshot()
fmt.Println(state.Pointer, state.WriteBacks, state.Frames)
```

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
go test ./enhancedclock
go test -run TestReplacementRules -v ./enhancedclock

# 查看逐步朴素模拟的输入、输出与判定日志
go test -run TestNaiveModel -v ./enhancedclock

# 并发测试与竞态检测
go test -race -run TestConcurrentAccessFlushQuery -v ./enhancedclock

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
