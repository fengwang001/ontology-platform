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

## 通知模板（`notify` 包）

`notify.Store` 实现通知模板的版本化存储与带语言/渠道回退的渲染。

### 语言标签与回退链

- 标签由 1–3 个以 `-` 连接的子标签组成，区分大小写：
  - 第 1 个：2 或 3 个小写字母（语言）。
  - 第 2 个：4 个字母且首字母大写（如 `Hant`，文字）或 2 个大写字母（如 `TW`，地区）。
  - 第 3 个只允许跟在文字之后，为 2 个大写字母。
- 回退链为：语言自身 → 依次去掉最后一个子标签 → 只剩第一子标签 → 默认语言 `dl`（已在链中则不重复），保持顺序。例如 `dl=en`、`loc=zh-Hant-TW` 的链是
  `zh-Hant-TW → zh-Hant → zh → en`；`zh-TW` 的链是 `zh-TW → zh → en`，不会经过 `zh-Hant`。

### 候选顺序与版本选择

- 渠道键为具体渠道 `email`、`sms`、`push` 与通用渠道 `*`。
- 候选序列以外层语言（按回退链顺序）、内层渠道（`[请求渠道, *]`）枚举。因此语言更贴近的 `*` 模板优先于语言更靠后的具体渠道模板。
- 对每个 `(name, loc, ch)`，取 `eff <= at` 的最大 `eff` 版本；无版本或该版本为墓碑则跳过该键（墓碑只跳过本键，不阻断回退）。
- 版本按 `eff` 升序保存，同 `eff` 后登记的覆盖先登记的（正文与墓碑互相覆盖）。版本查找使用二分，单键探测次数不超过 `ceil(log2(版本数+1))`；一次 `Render` 的键查找数不超过回退链长度的 2 倍。非导出计数器随测试断言这两条界。

### 正文语法、变量与转义

- `{{`、`}}` 分别渲染为字面 `{`、`}`。
- `{v}` 为必需变量（缺失即该候选失败；变量存在但值为空串算存在）。
- `{v|text}` 为带默认文本的变量：变量存在（含空串）时取其值，否则取 `text`；默认文本不转义。
- 其它单独出现的 `{` 或 `}` 为语法错误，按自左向右扫描报告首个错误字节偏移；`{{`/`}}` 优先按字面处理，但变量只以紧随的第一个 `}` 结束（`{a}}` = 变量 `a` 加单 `}`，偏移 3）。
- 转义只看**请求渠道**：渲染到 `email` 时仅变量值做 `& < > "` → `&amp; &lt; &gt; &quot;`（`*` 模板渲染到 email 同样生效）；默认文本与正文字面永不转义；`sms`/`push` 不转义。
- 长度按码点计数：`sms` 上限 70、`push` 上限 100、`email` 不限；恰等于上限成功，超限则该候选失败并回退。
- 第一个成功候选即结果，返回文本、命中语言、命中渠道键与版本 `eff`；全部失败时按**第一个候选**的原因报 `KindMissingVars`（变量名升序）或 `KindTooLong`（码点数）；无任何候选报 `KindNoTemplate`。

### 本地验证

```bash
# 规则用例 + 2000 组随机序列对拍（朴素实现线性枚举全部候选）
go test ./notify/

# 打印每组登记/渲染的输入、输出与判定依据（lookups、maxProbe）
go test -run TestDifferentialNaive -v ./notify/

# 竞态、vet 与格式
go test -race ./...
go vet ./...
gofmt -l .
```
