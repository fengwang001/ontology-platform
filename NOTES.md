# 命名风格互转推导

## 切分对照与缩写断点

| 输入 | 词序列（缩写标记） |
| --- | --- |
| `HTTPServer` | `HTTP`(缩写), `Server`(普通) |
| `parseXMLID` | `parse`(普通), `XMLID`(缩写) |
| `utf8Reader` | `utf`(普通), `8`(数字), `Reader`(普通) |
| `sha256Sum` | `sha`(普通), `256`(数字), `Sum`(普通) |
| `IDs` | `ID`(缩写), `s`(普通) |
| `aB` | `a`(普通), `B`(缩写) |

规则：小写/数字到大写断开；字母与数字互相断开；下划线显式断开。连续大写后接小写时，最后一个大写字母属于后词，所以断点在 `S` 前：`HTTP|Server`。这保证 `HTTPServer`、`httpServer`、`http_server` 的词文本均为 `http,server`。没有后续小写时，相邻缩写无法区分，故 `XMLID` 是一个缩写词。

`HTTPServer -> snake -> UpperCamel` 为 `http_server -> HttpServer`，不同于原串。蛇形会丢失“原词曾全大写”的信息；幂等只要求 `f(f(x))=f(x)`，不要求 `f` 可逆。往返恒等只在 `snake -> 任意风格 -> snake` 成立；任意风格经 snake 后可能得到规范驼峰词形。

## 四条不变量

1. 切分稳定：`scan.Scanner.Split` 用同一套词边界规则，蛇形仅增加下划线边界；由 `TestSplitEquivalence` 钉住。
2. 幂等：`style.Join` 只产生规范大小写，`api.Convert` 先切后拼；由 `TestIdempotence` 钉住。
3. 蛇形规范形：所有目标风格都从同一小写词序列拼装；由 `TestSnakeCanonical` 钉住。
4. 失败不留痕：`scan.Scanner.Split` 完成整串校验后才返回词，`api.Convert` 遇错返回空串；由 `TestRejectedInputs` 钉住。
