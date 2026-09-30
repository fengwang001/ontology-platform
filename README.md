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

## 分层合并键值存储（`lsm` 包）

写路径：写入先追加进内存缓存（memtable），缓存满则冻结成不可变段进入第 0 层；
某层段数达到扇出阈值（`Fanout`）时，把该层最旧的若干段合并进上一层；
最大层满时合并结果仍留在最大层。

### 层合并与墓碑保留规则

- 合并时同一个键只保留**段内序号（Seq）最大**的那条记录，墓碑与普通记录一同参与比较。
- 墓碑只有在**最大层自合并**时才可丢弃：此时被合并的是全系统最旧的段，
  不存在比它们更早的写入，墓碑已无需遮蔽任何记录。
- 向更深层合并（含合并进最大层）时，目标层可能仍有更旧的段，
  墓碑必须保留以遮蔽这些更旧的记录。
- 最大层合并结果比本层剩余段都旧，插入层首；向下一层合并的结果比目标层已有段都新，追加层尾，
  因此每层内段始终按时间有序，读取按“缓存 → 第 0 层 → 更深层、层内新到旧”返回首个命中。

### 一致性与并发

- 读视图是原子切换的快照（`atomic.Pointer`）：合并产生的新段全部落盘并提交清单后才切换，
  并发读任一时刻只能看到某次合并前或合并后的完整状态，不会读到混合两代的记录。
- 段文件先写临时文件再原子重命名；合并失败会清理临时文件，缓存、段与层分布保持不变。
- 空键（`ErrEmptyKey`）、非法参数（`ErrInvalidArgument`）、损坏段（`ErrCorruptSegment`）
  分别返回可区分的错误；段尾部的不完整记录会被安全截断，中间损坏则整体拒绝加载。

### 本地验证：按时间顺序重放核对

1. 维护一个内存模型 `map[string]string` 作为事实标准。
2. 按时间顺序把每个操作同时应用到模型与存储：`Put` 写入模型，`Delete` 从模型删除。
3. 重放结束后遍历全键空间逐键核对：模型中存在的键，`Get` 必须命中且值相等；
   模型中不存在（已删除或从未写入）的键，`Get` 必须未命中。
4. 再调用 `Check()` 自检：重新解码所有段并校验层级不变量。

对应测试见 `lsm/store_test.go` 与 `lsm/store_concurrent_test.go`
（层级级联、墓碑保留、段截断恢复、损坏拒绝、并发读、重放核对），
运行 `go test -race -v ./lsm/` 可看到每次写入、读取结果与判定依据的日志。
