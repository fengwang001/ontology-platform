六行切分对照表（词以 / 分隔，[缩]=全大写缩写）：
1. HTTPServer   -> HTTP[缩] / Server
2. parseXMLID   -> parse / XML[缩] / ID[缩]
3. utf8Reader   -> utf8 / Reader
4. sha256Sum    -> sha256 / Sum
5. IDs          -> I[缩] / Ds
6. aB           -> a / B[缩]

缩写断点规则：大写段后接小写段时，仅当该小写段长度>=2，才把最后一个
大写字母留给小写段（HTTP|Server；XML|ID 末尾不动）；小写段长度为1
（IDs 的 s、Ds）则不断，保证幂等且蛇形规范（inv-3）。数字并入相邻词。

HTTPServer -> snake -> UpperCamel = HttpServer（HTTP 信息在 snake 丢失，
与原串不同）。幂等仍成立：Convert 作用于其输出 HttpServer 仍是 HttpServer
（幂等只要求 f(f(x))=f(x)，不要求 f 可逆/往返恒等）。恒等方向仅
camel->snake->snake；snake->UpperCamel->snake 恒等；camel 经 snake 再
回 camel 不恒等，因为 snake 是有损规范形。

不变量（保证位置 / 钉住的测试）：
inv-1 切分稳定: scan.splitter.Split 同一词列 / TestSplitStability
inv-2 幂等:     api.Convert 一律经 snake 规范形 / TestIdempotent
inv-3 蛇形规范: api.Convert 以 Join(words,Snake) 为中转 / TestCanonicalSnake
inv-4 失败不留痕: scan.Split 边校验边累积、出错即返回哨兵错误 / TestErrors
