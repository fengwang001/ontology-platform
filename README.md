# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 窗口函数帧边界

`ontology.NewFrameCalculator(capacity)` 创建按单个排序键维护的分区：

- `Append(key)` 按非降序追加行，行下标从 0 开始；相等的键构成一个并列组。
- `Frame(i, spec)` 返回第 `i` 行排除处理后的帧，用升序、互不相邻的非空半开区间 `[start,end)` 表示。
- `Append` 使用写锁，`Frame` 使用读锁并复制本次计算所需快照；调用返回的区间切片不与内部存储共享。

三种模式的差值 `d` 分别是：

- `ROWS`：`d = j - i`。
- `RANGE`：`d = key[j] - key[i]`，比较按精确整数处理，即使差值不在 `int64` 内也不会回绕或饱和。
- `GROUPS`：`d = group[j] - group[i]`，并列组按键升序从 0 编号。

起点条件为：`UNBOUNDED PRECEDING` 恒真；`n PRECEDING` 为 `d >= -n`；`CURRENT ROW` 为 `d >= 0`；`n FOLLOWING` 为 `d >= n`。终点条件为：`n PRECEDING` 为 `d <= -n`；`CURRENT ROW` 为 `d <= 0`；`n FOLLOWING` 为 `d <= n`；`UNBOUNDED FOLLOWING` 恒真。

排除项先得到排除前帧，再应用：

- `NONE`：不删除。
- `CURRENT ROW`：删除第 `i` 行。
- `GROUP`：删除第 `i` 行所在并列组的全部行。
- `TIES`：删除同组中除第 `i` 行之外的并列行；第 `i` 行若原本不在帧内，也不会被补入。

相邻或重叠的保留行会合并成一个半开区间；删除中间行或中间并列组后会自然分裂为多个区间，空帧返回空序列。

构造、追加和查询的错误按接口规定的优先级只返回第一个原因。被拒绝的 `Append` 不会改变分区；非法 `FrameSpec` 与越界行号也不会改变状态。

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

# 2000 组随机分区与 spec 对拍；日志包含输入、输出与逐行判定依据
go test -run TestFrameMatchesNaiveImplementation -v ./ontology

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
