# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 字符三元组相似词项查找器

`ontology.Finder` 提供并发安全的 `Add(word)`、`Remove(word)` 与 `Similar(query, theta, k)`。

- 词项按 UTF-8 解码后的字符序列处理；必须非空、合法且不含 `U+0002`、`U+0003`。
- 填充序列为 `U+0002 U+0002 w U+0003 U+0003`，其中所有长度为 3 的相邻字符窗口构成三元组多重集合 `G(w)`。
- `|G(w)| = rune_count(w) + 2`；例如单字符词项也有 3 个三元组，`aaaa` 中的 `aaa` 计数为 2。
- 多重集合交 `I(a,b)` 对每个不同三元组取两边出现次数的较小值后求和。
- Dice 系数为 `2I/(|G(a)|+|G(b)|)`，输出既约分数字符串；比较与命中判定均使用整数。
- 查询字符数不超过 2 时，有效阈值为 `min(100, theta+20)`；其他长度使用 `theta`。
- 命中判定为 `200*I >= effectiveThreshold*(|G(q)|+|G(w)|)`，恰等命中，少 1 不命中。
- 命中先按 Dice 降序，分数相同按词项 UTF-8 字节序升序。
- 前缀折叠只检查当前已保留列表：若新命中与任一已保留词项按字符序列互为真前缀，则折叠到最先匹配的已保留词项，后者 `Folded` 加 1；已折叠词项不再参与比较，因此关系不传递。
- 折叠统计覆盖完整命中序列，然后才截取前 `k` 个结果。

错误按以下顺序返回且失败操作不改变词典：

- `Add`：`ErrInvalidWord`、`ErrDuplicateWord`。
- `Remove`：`ErrWordNotFound`。
- `Similar`：`ErrInvalidWord`、`ErrInvalidThreshold`（theta 不在 1 到 100）、`ErrInvalidLimit`（k 小于 1）。

### 倒排候选剪枝

每个不同三元组维护一个倒排表。`Similar` 只读取查询所包含的不同三元组倒排表，并把至少共享一个三元组的登记词项作为候选。测试通过包内非导出读计数验证：

- 读取数不超过查询的各不同三元组倒排表长度之和。
- 阈值至少为 1，所以 `I=0` 的词项不可能通过 `200I >= th*(...)`，不会进入候选。
- 1,000 与 100,000 个词项且含查询三元组的词项数相同两档下，读取数均为 9，不随无关词项增长。

读写由 `sync.RWMutex` 串行化；一次查询在一致快照上计算，结果只取决于当前词项集合，与登记顺序无关。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 在 Go 代码中导入并使用
import "ontology/ontology"

finder := ontology.NewFinder()
err := finder.Add("abcde")
results, err := finder.Similar("abcde", 30, 10)
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 查看随机对拍与倒排读取计数日志（2000 组朴素实现对拍）
go test -v ./ontology -run 'TestRandomizedComparisonWithNaive|TestInvertedReadCount'

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
