# API -> 网页对话纯文本兼容主链路说明

文档导航：[总览](../README.MD) / [架构说明](./ARCHITECTURE.md) / [接口文档](../API.md) / [测试指南](./TESTING.md)

> 本文档是 DS2API“把 OpenAI / Claude / Gemini 风格 API 请求兼容成 DeepSeek 网页对话纯文本上下文”的专项说明。
> 这是项目最重要的兼容产物之一。凡是修改消息标准化、tool prompt 注入、tool history 保留、文件引用、current input file、下游 completion payload 组装等行为，都必须同步更新本文档。

## 1. 核心结论

DS2API 当前的核心思路，不是把客户端传来的 `messages`、`tools`、`attachments` 原样转发给下游。

而是把这些高层 API 语义，统一压缩成 DeepSeek 网页对话更容易理解的三类输入：

1. `prompt`
   一个单字符串，里面带有角色标记、system 指令、历史消息、assistant reasoning 标签、历史 tool call XML 等。
2. `ref_file_ids`
   一个文件引用数组，承载附件、inline 上传文件，以及必要时被拆出去的历史文件。
3. 控制位
   例如 `thinking_enabled`、`search_enabled`、部分 passthrough 参数。

也就是说，项目最重要的兼容动作，是把“结构化 API 会话”翻译成“网页对话纯文本上下文 + 文件引用”。

## 2. 为什么这是核心产物

因为对下游来说，真正稳定的输入面不是 OpenAI/Claude/Gemini 的原生 schema，而是：

- 一段连续的对话 prompt
- 一组可引用文件
- 少量开关位

这也是为什么很多表面上看像“协议兼容”的代码，最终都会收敛到同一类逻辑：

- 先把不同协议的消息统一成内部消息序列
- 再把工具声明改写成 system prompt 文本
- 再把历史 tool call / tool result 改写成 prompt 可见内容
- 最后输出成 DeepSeek completion payload

## 3. 统一心智模型

当前主链路可以这样理解：

```text
客户端请求
  -> HTTP API surface（OpenAI / Claude / Gemini）
  -> promptcompat 统一消息标准化
  -> tool prompt 注入
  -> DeepSeek 风格 prompt 拼装
  -> 文件收集 / inline 上传（OpenAI 文件链路）
  -> current input file（completion runtime 全局入口）
  -> expert prompt segment（expert 模型超长提示词分段）
  -> completion payload
  -> 下游网页对话接口
  -> assistantturn 输出语义归一（Go 非流式 + 流式收尾）
  -> 各协议 renderer（OpenAI / Responses / Claude / Gemini）
```

对应的关键代码入口：

- OpenAI Chat / Responses：
  [internal/promptcompat/request_normalize.go](../internal/promptcompat/request_normalize.go)
- OpenAI prompt 组装：
  [internal/promptcompat/prompt_build.go](../internal/promptcompat/prompt_build.go)
- OpenAI 消息标准化：
  [internal/promptcompat/message_normalize.go](../internal/promptcompat/message_normalize.go)
- Claude 标准化：
  [internal/httpapi/claude/standard_request.go](../internal/httpapi/claude/standard_request.go)
- Claude 消息与 tool_use/tool_result 归一：
  [internal/httpapi/claude/handler_utils.go](../internal/httpapi/claude/handler_utils.go)
- Gemini 复用 OpenAI prompt builder：
  [internal/httpapi/gemini/convert_request.go](../internal/httpapi/gemini/convert_request.go)
- DeepSeek prompt 角色标记拼装：
  [internal/prompt/messages.go](../internal/prompt/messages.go)
- prompt 可见 tool history XML：
  [internal/prompt/tool_calls.go](../internal/prompt/tool_calls.go)
- 最新 user 思考格式注入：
  [internal/promptcompat/thinking_injection.go](../internal/promptcompat/thinking_injection.go)
- completion payload：
  [internal/promptcompat/standard_request.go](../internal/promptcompat/standard_request.go)
- Go 输出侧 assistant turn：
  [internal/assistantturn/turn.go](../internal/assistantturn/turn.go)
- Go completion runtime：
  [internal/completionruntime/nonstream.go](../internal/completionruntime/nonstream.go)

## 4. 下游真正收到的东西

在“完成标准化后”，下游 completion payload 的核心形态是：

```json
{
  "chat_session_id": "session-id",
  "model_type": "default",
  "parent_message_id": null,
  "prompt": "<System>:...",
  "ref_file_ids": [
    "file-history",
    "file-systemprompt",
    "file-other-attachment"
  ],
  "thinking_enabled": true,
  "search_enabled": false,
  "action": null,
  "preempt": false
}
```

重点是：

- `prompt` 才是对话上下文主载体。
- `ref_file_ids` 只承载文件引用，不承载普通文本消息。
- `tools` 不会作为“原生工具 schema”直接下发给下游，而是被改写进 `prompt`。
- 对外返回给客户端的 `prompt_tokens` / `input_tokens` / `promptTokenCount` 不再按“最后一条消息”或字符粗估近似返回，而是基于**完整上下文 prompt**做 tokenizer 计数；为了避免上下文实际超限但客户端误以为还能塞下，请求侧上下文 token 会额外保守上浮一点，宁可略大也不低估。
- 当前 `/v1/chat/completions` 业务路径仍是“每次请求新建一个远端 `chat_session_id`，并默认发送 `parent_message_id: null`”；因此 DS2API 对外默认表现为“新会话 + prompt 拼历史”，而不是复用 DeepSeek 原生会话树。
- 但 DeepSeek 远端本身支持同一 `chat_session_id` 的跨轮次持续对话。2026-04-27 已用项目内现有 DeepSeek client 做过一次不改业务代码的双轮实测：同一 `chat_session_id` 下，第 1 轮返回 `request_message_id=1` / `response_message_id=2` / 文本 `SESSION_TEST_ONE`；第 2 轮重新获取一次 PoW，并发送 `parent_message_id=2` 后，成功返回 `request_message_id=3` / `response_message_id=4` / 文本 `SESSION_TEST_TWO`。这说明“同远端会话持续聊天”能力存在，且每轮需要携带正确的 parent/message 链接信息，同时重新获取对应轮次可用的 PoW。
- OpenAI Chat / Responses 原生走统一 OpenAI 标准化与 DeepSeek payload 组装；Claude / Gemini 会尽量复用 OpenAI prompt/tool 语义，其中 Gemini 直接复用 `promptcompat.BuildOpenAIPromptForAdapter`。Go 主服务新增 `completionruntime` 启动层，统一执行 DeepSeek session/PoW/call；输出侧新增 `assistantturn` 语义层：非流式 OpenAI Chat / Responses / Claude / Gemini 会把 DeepSeek SSE 收集结果先归一成同一份 assistant turn，再分别渲染成各协议原生外形；流式 OpenAI Chat / Responses / Claude / Gemini 继续保持各协议实时 SSE framing，但最终收尾的 tool fallback、schema 归一、usage、empty-output / content-filter 错误语义同样由 `assistantturn` 判定。Claude / Gemini 的常规 Go 主路径不再依赖内部 `httptest` 转发到 OpenAI handler；`translatorcliproxy` 仅保留用于 Vercel bridge、后端缺失 fallback 和回归测试，不作为主业务协议转换中心。
- Vercel Node 流式路径本轮不迁移，仍使用现有 Node bridge / stream-tool-sieve 实现；后续若变更 Node 流式语义，需要按 `assistantturn` 的 Go canonical 输出语义同步对齐。
- 客户端传入的 thinking / reasoning 开关会被归一到下游 `thinking_enabled`。Gemini `generationConfig.thinkingConfig.thinkingBudget` 会翻译成同一套 thinking 开关；关闭时即使上游返回 `response/thinking_content`，兼容层也不会把它当作可见正文输出。若最终解析出的模型名带 `-nothinking` 后缀，则会无条件强制关闭 thinking，优先级高于请求体中的 `thinking` / `reasoning` / `reasoning_effort`。未显式关闭时，各 surface 会按解析后的 DeepSeek 模型默认能力开启 thinking，并用各自协议的原生形态暴露：OpenAI Chat 为 `reasoning_content`，OpenAI Responses 为 `response.reasoning_text.delta` / `reasoning` item，Claude 为 `thinking` block / `thinking_delta`，Gemini 为 `thought: true` part。
- 对 OpenAI Chat / Responses 的非流式收尾，如果最终可见正文为空，兼容层会优先尝试把思维链中的独立 EPSE / XML 工具块当作真实工具调用解析出来。流式链路也会在收尾阶段做同样的 fallback 检测，但不会因为思维链内容去中途拦截或改写流式输出；真正的工具识别始终基于原始上游文本，而不是基于“已经做过可见输出清洗”的版本。最终可见层会剥离已经成功解析成工具调用的完整 leaked EPSE / XML `tool_calls` wrapper；如果遇到完整 wrapper 但内部形态不符合可执行工具调用语义（例如 `<param>` 这类 malformed XML 工具壳），流式 sieve 会把该块作为普通文本释放，而不是吞掉或伪造成工具调用。补发结果会作为本轮 assistant 的结构化 `tool_calls` / `function_call` 输出返回，而不是塞进 `content` 文本；如果客户端没有开启 thinking / reasoning，思维链只用于检测，不会作为 `reasoning_content` 或可见正文暴露。只有正文为空且思维链里也没有可执行工具调用时，才继续按空回复错误处理。
- OpenAI Chat / Responses、Claude Messages、Gemini generateContent 的空回复错误处理会做内部补偿重试：第一次上游完整结束后，如果最终可见正文为空、没有解析到工具调用、也没有已经向客户端流式发出工具调用，并且终止原因不是 `content_filter`，兼容层会复用同一个 `chat_session_id`、账号、token 与工具策略，把原始 completion `prompt` 追加固定后缀 `Previous reply had no visible output. Please regenerate the visible final answer or tool call now.` 后重新提交。同一账号内的补偿重试轮数由 `DS2API_EMPTY_OUTPUT_RETRY_MAX_ATTEMPTS` 配置（未设置或值非法时回退默认 3）；上游限流常表现为「只回 reasoning、正文为空」，单轮重试很难跨过限流窗口，因此默认允许多轮同账号重试。Go 主路径的非流式重试由 `completionruntime.ExecuteNonStreamWithRetry` 统一处理；流式重试由 `completionruntime.ExecuteStreamWithRetry` 统一处理，各协议 runtime 只负责消费/渲染本协议 SSE framing。重试遵循 DeepSeek 多轮对话协议：从第一次上游 SSE 流中提取 `response_message_id`，并在重试 payload 中设置 `parent_message_id` 为该值，使重试成为同一会话的后续轮次而非断裂的根消息；同时重新获取一次 PoW（若 PoW 获取失败则回退到原始 PoW）。该同账号重试不会重新标准化消息、不会新建 session，也不会向流式客户端插入重试标记；第二次 thinking / reasoning 会按正常增量直接接到第一次之后，并继续使用 overlap trim 去重。若同账号补偿重试后即将返回 429 `upstream_empty_output`，并且当前是托管账号模式，runtime 会在返回 429 前切换到下一个可用账号，新建 `chat_session_id`，使用原始 completion payload 再做一次 fresh retry；该切号重试不携带空回复 prompt 后缀，也不设置上一账号的 `parent_message_id`。如果 current input file 已触发，切号前会在新账号上重新上传同一份 `HISTORY.txt`（以及需要时的 `TOOLS.txt`），并用新账号可见的 file_id 替换自动生成的旧 file_id；客户端原本传入的其他文件引用保持不变。如果没有可切换账号，或切号后的 fresh retry 仍没有可见正文或工具调用，则继续按原错误返回：无任何输出为 503 `upstream_unavailable`，有 reasoning 但没有可见正文或工具调用为 429 `upstream_empty_output`。若任一尝试触发空 `content_filter`，不做补偿重试并保持 `content_filter` 错误。Vercel Node 流式路径通过 Go 内部 prepare / pow / switch 端点获取初始 payload、重试 PoW 和切号 fresh retry payload，因此同样会重新上传 current-input 自动文件并替换为新账号 file_id。

- 非流式 OpenAI Chat / Responses、Claude Messages、Gemini generateContent 在最终可见正文渲染阶段，会把 DeepSeek 搜索返回中的 `[citation:N]` / `[reference:N]` 标记替换成对应 Markdown 链接。`citation` 标记按一基序号解析；`reference` 标记只有在同一段正文中出现 `[reference:0]`（允许冒号后有空格）时才按零基序号映射，并且不会影响同段正文里的 `citation` 标记。
- 流式输出仍默认隐藏 `[citation:N]` / `[reference:N]` 这类上游内部标记，避免分片输出中泄漏尚未完成映射的引用占位符。

## 5. prompt 是怎么拼出来的

OpenAI Chat / Responses 在标准化后、current input file 之前，会默认执行 `thinking_injection` 增强。它参考 DeepSeek V4 "把控制指令放在 user 消息末尾更稳定"的用法，在最新 user message 后追加思考增强提示词。当前内置默认提示词以 `Reasoning Effort: Absolute maximum with no shortcuts permitted.` 开头，并继续要求模型充分分解问题、覆盖潜在路径与边界条件、把完整推演过程显式写出。该开关默认关闭，可通过 `thinking_injection.enabled=true` 开启；也可以通过 `thinking_injection.prompt` 自定义提示词，留空时使用内置默认提示词。

这段增强属于 prompt 可见上下文：

- 普通请求会直接出现在最终 `prompt` 的最新 user block 末尾。
- 如果触发 current input file，它会进入完整上下文文件中。

### 5.1 角色标记

最终 prompt 使用纯文本角色标记，以 `<>:` 包裹：

- `<System>:`
- `<User>:`
- `<Assistant>:`
- `<Tool>:`

每个角色块以对应标记开头，紧跟内容文本，不再有 begin/end 分隔符。

实现位置：
[internal/prompt/messages.go](../internal/prompt/messages.go)

### 5.2 相邻同角色消息会合并

在最终 `MessagesPrepareWithThinking` 中，相邻同 role 的消息会被合并成一个块，中间插入空行。

这意味着：

- prompt 中看到的是“合并后的 role block”
- 不是客户端传来的逐条 message 原样排列

## 6. tools 为什么是“文本注入”，不是原生下发

当前项目把工具能力视为“prompt 约束的一部分”。

具体做法：

1. 把每个 tool 的名称、描述、参数 schema 序列化成文本。
2. 拼成 `You have access to these tools:` 大段说明。
3. 再附上统一的 EPSE tool call 外壳格式约束。
4. 普通直传请求会把“工具描述 + 格式约束”一起并入 system prompt；如果 `current_input_file` 触发，则工具描述/schema 会单独上传成 `TOOLS.txt`，live prompt 和 system tool 格式提示都会明确要求模型把 `TOOLS.txt` 当作可调用工具和参数 schema 的权威来源。

工具调用正例现在优先示范半角管道符 EPSE 风格：`<|EPSE|tool_calls>` → `<|EPSE|invoke name="...">` → `<|EPSE|parameter name="...">`。
统一工具调用格式说明（`internal/toolcall/tool_prompt.go` 的 `BuildToolCallInstructions`）的规则文案、正反例标题已改为中文，但 EPSE 标签语法本身保持 ASCII（`<|EPSE|...>` / `invoke` / `parameter` / `CDATA` / 标点字符集 `< > / = " |` 等），中文文案仅用于规则解释与示例标题，不影响标签解析、容错归一与 schema 校验逻辑。
兼容层仍接受旧式纯 `<tool_calls>` wrapper，并会容错若干 EPSE 标签变体，包括短横线形式 `<epse-tool-calls>` / `<epse-invoke>` / `<epse-parameter>`、下划线形式 `<epse_tool_calls>` / `<epse_invoke>` / `<epse_parameter>`，以及其他前缀分隔形态如 `<vendor|tool_calls>` / `<vendor_tool_calls>` / `<vendor - tool_calls>`；标签壳扫描还会把全角 ASCII 漂移归一化，例如 `<ｅｐＳＥ|tool_calls>` 与全角 `＞` 结束符，也会容错 CJK 尖括号、全角感叹号或顿号分隔符、弯引号属性值、PascalCase 本地名和属性尾部分隔符漂移，例如 `<EPS|parameter name="command"|>...〈/EPS|parameter〉`、`<！EPSE！invoke name=“Bash”>`、`<、EPSE、tool_calls>`、`<DSmartToolCalls>`、`<EPSEtool_calls※>`。更一般地，Go / Node tag 扫描以固定本地标签名 `tool_calls` / `invoke` / `parameter` 为准，标签名前或标签名后的非结构性协议分隔符都会在解析入口剥离，例如 `<EPSE␂tool_calls>`、`<proto💥tool_calls>` 这类控制符或非 ASCII 分隔符漂移也会归一化回现有 XML 标签后继续走同一套 parser；结构性字符如 `<` / `>` / `/` / `=` / 引号、空白和 ASCII 字母数字不会被当作这类分隔符。进入现有 EPSE rewrite / XML parse 之前，Go / Node 还会先对“已经识别成工具标签壳的 candidate span”做一次窄 canonicalization：只折叠 wrapper / `invoke` / `parameter` / `name` / `CDATA` / `EPSE` 及其壳层分隔符里的 confusable 字符，清理零宽 / BOM / 控制类干扰，并把引号、空白、dash / underscore 变体等统一回可解析的工具语法。这个阶段不会广义改写普通正文、参数内容、Markdown 行内 code span、CDATA 里的示例文本或其他非工具 XML。CDATA 开头也使用同一类扫描式容错，`<![CDATA[` / `<！[CDATA[` / `<、[CDATA[` 都会作为参数原文容器处理。但提示词会优先要求模型输出官方 EPSE 标签，并强调不能只输出 closing wrapper 而漏掉 opening tag。需要注意：这是“兼容 EPSE 外壳，内部仍以 XML 解析语义为准”，不是原生 EPSE 全链路实现。解析器会先截获非 Markdown 代码上下文中的疑似工具 wrapper，完整解析失败或工具语义无效时再按普通文本放行。
数组参数使用 `<item>...</item>` 子节点表示；当某个参数体只包含 item 子节点时，Go / Node 解析器会把它还原成数组，避免 `questions` / `options` 这类 schema 中要求 array 的参数被误解析成 `{ "item": ... }` 对象。除此之外，解析器还会回收一些更松散的列表写法，例如 JSON array 字面量或逗号分隔的 JSON 项序列，只要它们足够明确；但 `<item>` 仍然是首选形态。若模型把完整结构化 XML fragment 误包进 CDATA，兼容层会在保护 `content` / `command` 等原文字段的前提下，尝试把非原文字段中的 CDATA XML fragment 还原成 object / array。不过，如果 CDATA 只是单个平面的 XML/HTML 标签，例如 `<b>urgent</b>` 这种行内标记，兼容层会保留原始字符串，不会强行升成 object / array；只有明显表示结构的 CDATA 片段，例如多兄弟节点、嵌套子节点或 `item` 列表，才会触发结构化恢复。对 `command` / `content` 等长文本参数，CDATA 内部的 Markdown fenced EPSE / XML 示例会作为原文保护；示例里的 `]]></parameter>` 或 `</tool_calls>` 不会截断外层工具调用，解析器会继续等待围栏外真正的参数 / wrapper 结束标签。
Go 侧读取 DeepSeek SSE 时不再依赖 `bufio.Scanner` 的固定 2MiB 单行上限；当写文件类工具把很长的 `content` 放在单个 `data:` 行里返回时，非流式收集、流式解析和 auto-continue 透传都会保留完整行，再进入同一套工具解析与序列化流程。
在 assistant 最终回包阶段，如果某个 tool 参数在声明 schema 中明确是 `string`，兼容层会在把解析后的 `tool_calls` / `function_call` 重新序列化成 OpenAI / Responses / Claude 可见参数前，递归把该路径上的 number / bool / object / array 统一转成字符串；其中 object / array 会压成紧凑 JSON 字符串。这个保护只对 schema 明确声明为 string 的路径生效，不会改写本来就是 `number` / `boolean` / `object` / `array` 的参数。这样可以兼容 DeepSeek 输出了结构化片段、但上游客户端工具 schema 又严格要求字符串参数的场景（例如 `content`、`prompt`、`path`、`taskId` 等）。
工具 schema 的权威来源始终是**当前请求实际携带的 schema**，而不是同名工具在其他 runtime（Claude Code / OpenCode / Codex 等）里的默认印象。兼容层现在会同时兼容 OpenAI 风格 `function.parameters`、直接工具对象上的 `parameters` / `input_schema`、以及 camelCase 的 `inputSchema` / `schema`，并在最终输出阶段按这份请求内 schema 决定是保留 array/object，还是仅对明确声明为 `string` 的路径做字符串化。该规则同样适用于 Claude 的流式收尾和 Vercel Node 流式 tool-call formatter，避免不同 runtime 因 schema shape 差异而出现同名工具参数类型漂移。
正例中的工具名只会来自当前请求实际声明的工具；如果当前请求没有足够的已知工具形态，就省略对应的单工具、多工具或嵌套示例，避免把不可用工具名写进 prompt。
对执行类工具，脚本内容必须进入执行参数本身：`Bash` / `execute_command` 使用 `command`，`exec_command` 使用 `cmd`；不要把脚本示范成 `path` / `content` 文件写入参数。
工具提示词也会明确要求模型按本次调用实际需要填写参数，禁止输出 placeholder、空字符串或纯空白参数；如果必填参数未知，应先追问用户或正常文字回复，而不是输出空工具壳。对 `Bash` / `execute_command` 这类 shell 工具，命令或脚本必须写入 `command` 参数。解析层仍会把空字符串参数结构化返回；是否拒绝空 `command` 由后续工具执行侧 / 客户端 schema 校验决定。
如果当前请求声明了 `Read` / `read_file` 这类读取工具，兼容层会额外注入一条 read-tool cache guard：当读取结果只表示“文件未变更 / 已在历史中 / 请引用先前上下文 / 没有正文内容”时，模型必须把它视为内容不可用，不能反复调用同一个无正文读取；应改为请求完整正文读取能力，或向用户说明需要重新提供文件内容。这个约束只缓解客户端缓存返回空内容导致的死循环，DS2API 不会也无法凭空恢复客户端本地文件正文。

OpenAI Chat / Responses 流式链路中的 toolSieve 拦截恒为开启（`bufferToolContent` 不再依赖客户端是否携带 `tools`；Vercel Node 流式路径的 `toolSieveEnabled` 与之一致）：模型被要求在回复末尾以 `<|EPSE|tool_calls>...` 格式输出工具调用，当客户端发起不带 `tools` 的「继续会话」请求时，若拦截关闭会导致 EPSE 原文作为正文透传给客户端形成乱码；toolSieve 的解析不依赖工具名过滤，空工具列表同样能正常拦截。已发出 `tool_calls` 之后，工具调用块之后追加的尾巴正文不再透传给客户端（OpenAI 规范中 tool_calls 回合的 content 应为空/缺省），Go 与 Vercel Node 两条流式路径行为一致；该丢弃只作用于流式输出层，完整模型原文仍保留在内部历史归档与 usage 统计中，供后续「继续会话」上下文使用。

统一工具调用格式说明（`internal/toolcall/tool_prompt.go` 的 `BuildToolCallInstructions`）还包含一段「调用决策」指引：历史对话中的 `<|EPSE|tool_calls>` 块是已执行完毕的工具调用记录而非待办事项；是否需要继续调用取决于当前任务还需什么，同一工具允许用不同参数多次调用、失败或结果不完整时也可重试，但不要因历史中出现过某工具而回避再次调用，也不要重复执行已完成且结果正确的调用。该段与原有格式规则并存，未改变标签语法、参数格式或示例强度。

### 6.1 按 API Key 控制工具注入

每个托管 API Key 可以单独配置 `tools_enabled` 开关（默认关闭）。当某把 Key 的 `tools_enabled=false` 时，使用该 Key 发起的请求无论是否携带 `tools` 定义，兼容层都会在标准化之前清空 `tools` / `tool_choice` 字段，使得：

- 工具描述（`You have access to these tools:` 大段文本）不会被注入 system prompt
- EPSE 工具调用格式说明、正面/反面示例不会注入（包括「调用决策」指引段落）
- `TOOLS.txt` 工具描述文件不会上传

说明：OpenAI Chat / Responses 流式链路的 toolSieve 拦截本身恒为开启（安全兜底，不随 `tools_enabled` 切换），但 `tools_enabled=false` 下 EPSE 工具指令与 `TOOLS.txt` 均不注入，模型不会被引导输出可拦截的工具块，因此实际不会产生工具调用。

仅保留最基础的文字对话能力。该开关适用于降低封号风险的场景。

直传 token 模式（caller key 不在 `config.api_keys` 中）不受此开关影响，始终保持原有工具注入行为。

实现：
- [internal/auth/request.go](../internal/auth/request.go) `Resolver.ToolsEnabledForRequest`
- [internal/config/store.go](../internal/config/store.go) `Store.APIKeyToolsEnabled`

OpenAI 路径实现：
[internal/promptcompat/tool_prompt.go](../internal/promptcompat/tool_prompt.go)

Claude 路径实现：
[internal/httpapi/claude/handler_utils.go](../internal/httpapi/claude/handler_utils.go)

统一工具调用格式模板：
[internal/toolcall/tool_prompt.go](../internal/toolcall/tool_prompt.go)

这也是项目“网页对话纯文本兼容”的关键设计：

- tools 对下游来说，本质上是 prompt 内规则
- 不是 native tool schema transport

### 6.2 按账号号池类型过滤调度

每个 DeepSeek 账号可以独立配置号池类型（`pool_type`），控制该账号可被哪类工具请求调用：

| `pool_type` | 含义 | `tools_enabled=true` 的 Key | `tools_enabled=false` 的 Key |
|---|---|---|---|
| `default` | 默认号池，允许无工具和含工具调用 | 可调用 | 可调用 |
| `no_tools` | 仅允许无工具调用 | 不可调用 | 可调用 |
| `tools_only` | 仅允许含工具调用 | 可调用 | 不可调用 |

未设置 `pool_type` 的旧账号视为 `default`，行为不变。

调度逻辑：

- 受管 API Key 请求在获取账号时，会根据该 Key 的 `tools_enabled` 状态构造一个 filter，只调度到 `MatchesPoolType` 返回 `true` 的账号。
- 轮询模式下跳过不匹配的账号；若所有可用账号都不匹配，直接返回 `no accounts` 错误，不会无限排队。
- 排队等待仅在存在匹配候选账号时才允许；无匹配候选时不排队。
- `X-Ds2-Target-Account` 指定账号时同样应用 filter：若指定账号的号池类型与请求工具开关不匹配，返回错误。
- 账号切换重试（`SwitchAccount`）也会携带原始请求的 `ToolsEnabled`，保证切换后仍遵守号池约束。
- 直传 token 模式不经过账号池，号池设置对其无影响。

实现：
- [internal/config/account.go](../internal/config/account.go) `Account.MatchesPoolType` / `NormalizePoolType`
- [internal/account/pool_acquire.go](../internal/account/pool_acquire.go) `AccountFilter` / `Acquire` / `AcquireWait`
- [internal/auth/request.go](../internal/auth/request.go) `acquireManagedRequestAuth` / `SwitchAccount`

## 7. assistant 的 tool_calls / reasoning 如何保留

### 7.1 reasoning 保留方式

assistant 的 reasoning 会变成一个显式标签块：

```text
[reasoning_content]
...
[/reasoning_content]
```

然后再接可见回答正文。

对最终返回给客户端的 assistant 轮次，reasoning 不会因为本轮输出了工具调用而被丢弃。OpenAI Chat 会在同一个 assistant message 上同时返回 `reasoning_content` 和 `tool_calls`；OpenAI Responses 会先返回一个包含 `reasoning` content 的 assistant message item，再返回后续 `function_call` item；Claude / Gemini 也会在各自原生 thinking / thought 结构后继续返回 tool_use / functionCall。

对进入后续 prompt / `HISTORY.txt` 的历史轮次，兼容层也会把同一轮工具调用前的 reasoning 绑定到 assistant tool call 历史上。OpenAI Chat 原生 `reasoning_content + tool_calls` 会直接保留；OpenAI Responses 若以 `reasoning` message item 后接 `function_call` item 的形式回放历史，会在归一化时合并为同一个 assistant 历史块；Claude 的 `thinking` block 会绑定到后续 `tool_use`；Gemini 的 `thought: true` part 会绑定到后续 `functionCall`。最终 prompt 中的顺序固定为 `[reasoning_content]...[/reasoning_content]`，再接 EPSE tool call 外壳。

### 7.2 历史 tool_calls 保留方式

assistant 历史 `tool_calls` 不会保留成 OpenAI 原生 JSON，而会转成 prompt 可见的 EPSE 外壳：

```xml
<|EPSE|tool_calls>
  <|EPSE|invoke name="read_file">
    <|EPSE|parameter name="path"><![CDATA[src/main.go]]></|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>
```

如果客户端历史里没有结构化 `tool_calls` 字段、却把一个可独立解析的 assistant 工具块放进了普通 `content`，兼容层会在写入后续 prompt 前先按工具调用解析它，再重渲染为规范 EPSE 历史外壳。这样可以避免一次 malformed 工具块未被结构化保存后，作为普通 assistant 文本回灌，继续污染后续模型的 few-shot 工具格式。

解析层同时兼容旧式纯 XML 形态：`<tool_calls>` / `<invoke>` / `<parameter>`。两者都会先归一到现有 XML 解析语义；其他旧格式都会作为普通文本保留，不会作为可执行调用语法。
例外是 parser 会对一个非常窄的模型失误做修复：如果 assistant 输出了 `<invoke ...>` ... `</tool_calls>`（或 EPSE 对应标签），但漏掉最前面的 opening wrapper，解析阶段会在 wrapper-confidence 足够高时补回 wrapper 后再尝试识别。这里的 wrapper-confidence 指 scanner 已经识别出白名单工具壳结构，剩余失败只像壳层结构漂移，而不是语义上接近但不在白名单内的 near-miss 标签名。修复成功时，wrapper 后面的 suffix prose 会继续保留在可见文本里；修复失败时，该块仍按普通文本处理。

这件事很重要，因为它决定了：

- 历史工具调用在 prompt 中是“可见文本历史”
- 不是“隐藏结构化元数据”

实现位置：
[internal/prompt/tool_calls.go](../internal/prompt/tool_calls.go)

### 7.3 tool result 保留方式

tool / function role 的结果会作为 `<Tool>:...` 进入 prompt。

如果 tool content 为空，当前会补成字符串 `"null"`，避免整个 tool turn 丢失。

## 8. files、附件、systemprompt 文件的实际语义

这里要明确区分两类东西：

1. 文本型 system prompt
   例如 OpenAI `developer` / `system` / Responses `instructions` / Claude top-level `system`
   这类会进入 `prompt`。
2. 文件型 systemprompt
   例如通过附件、`input_file`、base64、data URL 上传的文件
   这类不会直接内联进 `prompt`，而是进入 `ref_file_ids`。

OpenAI 文件相关实现：

- inline/base64/data URL 上传：
  [internal/httpapi/openai/files/file_inline_upload.go](../internal/httpapi/openai/files/file_inline_upload.go)
- 文件 ID 收集：
  [internal/promptcompat/file_refs.go](../internal/promptcompat/file_refs.go)
- 自动路由视觉模型（`auto_route_vision`）：
  [internal/promptcompat/image_route.go](../internal/promptcompat/image_route.go)

OpenAI 的文件上传现在不再是"只传文件本体"的通用路径，而是会先根据请求里的 `model` 解析出 DeepSeek 的上传类型，并把它透传到上传接口的 `x-model-type`。当前可见的上传类型就是 `default` / `expert` / `vision`，其中 vision 请求上传图片时必须带上 `vision`，否则下游容易退回到仅文本或 OCR 语义。expert（pro）模型不支持文件上传，runtime 会在内联文件预处理和 current input file 阶段直接跳过，completion payload 的 `ref_file_ids` 也会被清空。这个模型类型会同时用于：

- `/v1/files` 这类独立文件上传入口
- Chat / Responses 的 inline 图片、附件上传
- current input file 触发时生成的 `HISTORY.txt` 上下文文件

也就是说，文件上传和完成请求的 `model_type` 现在是一致的：完成 payload 里仍然是 `model_type`，上传文件则会在 DeepSeek 上传阶段携带同样的模型类型信息。

结论：

- “systemprompt 文字”在 prompt 里
- “systemprompt 文件”通常只在 `ref_file_ids` 里

除非调用方自己把文件内容展开后再塞进 system/developer 文本，否则文件内容不会自动出现在 prompt 正文。

### 8.1 自动路由视觉模型（`auto_route_vision`）

`auto_route_vision` 是行为设置中的开关，默认关闭。开启后，当客户端选择非 vision 模型（如 `deepseek-flash`、`deepseek-v4-pro` 及其 alias）且**当前用户轮次**携带图片内容时，DS2API 会把本次请求临时路由到 `deepseek-vision`（若原模型带 `-nothinking` 后缀则映射到 `deepseek-vision-nothinking`），同时把图片块从 prompt 消息中剔除，仅保留已上传的文件引用。历史消息中的图片不会触发本次路由，因此不带新图片的 follow-up 请求会自动回到用户原来选择的模型。

处理顺序：

1. 在 OpenAI Chat / Responses handler 解码请求后，先调用 `promptcompat.MaybeAutoRouteVision` 判断是否需要切换模型；若需要，把 `req["model"]` 改写成 vision 模型。
2. 随后走 `PreprocessInlineFileInputs` 上传 inline 图片；因为此时 `model` 已经是 vision，上传会带上 `model_type=vision`，确保下游视觉模型正确识别图片。
3. 上传完成后调用 `promptcompat.StripImageBlocksFromRequest` 把消息中的图片块剔除，但 `ref_file_ids` 中仍然保留图片文件 ID。
4. 标准化得到 `StandardRequest` 后，把 `RequestedModel` / `ResponseModel` 恢复成客户端最初请求的模型名，因此客户端看到的响应模型不变。
5. 该路由仅作用于本次请求；后续无图片请求会继续使用客户端原本选择的模型。

当前实现主要面向 OpenAI Chat / Responses 路径，因为该路径具备 inline 图片上传与 `ref_file_ids` 机制。Claude / Gemini 路径在配置接口层面已暴露该开关，但图片上传与剔除逻辑暂沿用各自现有转换行为。

相关实现：

- 路由与图片检测/剔除：
  [internal/promptcompat/image_route.go](../internal/promptcompat/image_route.go)
- OpenAI Chat 接入点：
  [internal/httpapi/openai/chat/handler_chat.go](../internal/httpapi/openai/chat/handler_chat.go)
- OpenAI Responses 接入点：
  [internal/httpapi/openai/responses/responses_handler.go](../internal/httpapi/openai/responses/responses_handler.go)

## 9. 多轮历史为什么不会一直完整内联在 prompt

兼容层现在只保留 `current_input_file` 这一种拆分方式；旧的 `history_split` 配置字段已移除，读取旧配置时会忽略它且不会再写回。

- `current_input_file` 默认关闭；它在统一 completion runtime 入口全局生效，用于把“完整上下文”合并进 `HISTORY.txt` 上下文文件。当最新 user turn 的纯文本长度达到 `current_input_file.min_chars`（默认 `0`）时，runtime 会上传一个文件名为 `HISTORY.txt` 的上下文文件。文件内容会先经过各协议入口的标准化，再序列化成按轮次编号的 `HISTORY.txt` 风格 transcript，带有 `# HISTORY.txt` 标题和 `=== N. ROLE ===` 分段；在 `Prior conversation history and tool progress.` 描述行之后紧挨着插入一段 continuation 说明（“从 HISTORY.txt 的最新状态继续推进”），该说明不再注入 live prompt。如果当前请求声明了可用工具，还会把工具名称、描述和参数 schema 单独上传成 `TOOLS.txt`，带有 `# TOOLS.txt` 标题。live prompt 中则只保留一个极短的 `继续会话` user 消息，并在有工具文件时明确可用工具 schema 位于 `TOOLS.txt`；system prompt 也会在统一 EPSE 工具格式约束前说明 `TOOLS.txt` 是可调用工具和 schema 的权威来源，同时保留本轮工具选择策略，避免把任务拉回起点。
- 如果 `current_input_file.enabled=false`，请求会直接透传，不上传任何拆分上下文文件。
- expert（pro）模型不支持文件上传。即使 `current_input_file` 已开启且达到阈值，runtime 也不会为 expert 模型上传 `HISTORY.txt` / `TOOLS.txt`；同时客户端传入的所有 `ref_file_ids` 和内联文件附件也会在 completion payload 中被丢弃（不会发送给上游）。
- 即使触发 `current_input_file` 后 live prompt 被缩短，对客户端回包里的上下文 token 统计，仍会沿用**拆分前的完整 prompt 语义**做计数，而不是按缩短后的占位 prompt 计算；否则会把真实上下文显著算小。

相关实现：

- 配置访问器：
  [internal/config/store_accessors.go](../internal/config/store_accessors.go)
- 当前输入转文件：
  [internal/httpapi/openai/history/current_input_file.go](../internal/httpapi/openai/history/current_input_file.go)
- 全局 completion runtime 应用点：
  [internal/completionruntime/nonstream.go](../internal/completionruntime/nonstream.go)

当前输入转文件启用并触发时，上传的历史文件真实文件名是 `HISTORY.txt`，文件内容是完整 `messages` 上下文；它会使用 OpenAI-compatible 的消息/transcript 序列化规则和 DeepSeek 角色标记，再按轮次编号成 `HISTORY.txt` 风格的 transcript（不再注入文件边界标签）：

```text
[uploaded filename]: HISTORY.txt
# HISTORY.txt
Prior conversation history and tool progress.
Continue from the latest state in the attached HISTORY.txt context. Treat it as the current working state and answer the latest user request directly.Do not mention HISTORY.txt in the main text.

=== 1. SYSTEM ===
...

=== 2. USER ===
...

=== 3. ASSISTANT ===
...

=== 4. TOOL ===
...
```

如果当前请求带有工具，runtime 同时上传 `TOOLS.txt`：

```text
[uploaded filename]: TOOLS.txt
# TOOLS.txt
Available tool descriptions and parameter schemas for this request.

You have access to these tools:

Tool: ...
Description: ...
Parameters: ...
```

开启后，请求的 live prompt 不再直接内联完整上下文，也不再内联大段工具 schema；它只保留一个极短的 `继续会话` user 消息，并在有工具时引用 `TOOLS.txt`；引导模型从 `HISTORY.txt` 最新状态继续推进的 continuation 说明紧跟在上传的 `HISTORY.txt` 文件顶部描述行之后。上传后的 `HISTORY.txt` file_id 会排在 `ref_file_ids` 最前；如果存在 `TOOLS.txt`，它的 file_id 紧随其后；客户端已有的其他 file_id 保持在后面。上下文 token 统计会包含上传的历史文件、工具文件和 live prompt。自动生成的 current-input 文件引用会被记录为 runtime 状态；如果托管账号模式切号 fresh retry，runtime 会重新上传这些自动文件，而不是把上一账号的 file_id 交给新账号。对于 expert（pro）模型，`ref_file_ids` 会在 completion payload 中被清空，且不会上传任何 current-input 文件。

设计经验：凡是注入到上游会话里的固定提示词，都要尽量克制、自然。官方一旦识别出它是“为反代服务准备的专属指令”，就可能触发封号。所以能不加就不加；确实需要注入时，也要优先选普通对话里常见的说法（例如 `继续会话`，而不是结构化的英文长指令），措辞必须贴近用户真实会说的话，不能显得像系统指令。同时措辞也不宜过于简短——之前只写 `继续` 两个字时，模型偶尔会短暂困惑、接不上任务；换成 `继续会话` 这种仍然大众、但又把语义点满的短语后行为才稳定。简单说：保持自然、大众化、语义足够，且不要让官方看出这句话是在为代理服务兜底。

### 9.1 expert 模式提示词分段（expert_prompt_segment）

因为 expert（pro）模型不支持文件上传，`current_input_file` 拆分方式无法为 expert 模型缩短 live prompt。当 expert 模型的 `FinalPrompt` 超过字符数阈值时，兼容层会按 rune 字数把提示词切分为多段，不再按 `<User>` / `<Assistant>` 等 role 标记边界切分；这些标记在 DeepSeek Web Chat 中只是普通文本。前 N-1 段使用 `FireCompletionAndStop`（发送后捕获 `response_message_id` 再调用 `stop_stream` 终止生成），最后一段正常返回完整响应。该机制复用 `StartCompletionWithSegments` 编排，对等 `StartCompletion` 可无缝接入现有流式与非流式 `Execute*` 流程。

- `expert_prompt_segment` 默认开启；仅对 `model_type == "expert"` 的模型生效。
- `expert_prompt_segment.max_chars`（默认 `160000`）是分段阈值，以 rune 字符数统计 `FinalPrompt`（含所有 role 标记和工具提示词）。
- `FireCompletionAndStop` 捕获 `response_message_id` 后会继续消费 SSE，等待首批实际内容（`response/content`、`response/thinking_content` 或 `response/fragments`）到达后立即调用 `stop_stream`；若内容等待超时则直接报错终止后续段发送，不再兜底停止。`stop_stream` 后等待上游 `event: close` 确认消息已落库，再短暂 settle 后发送下一段，避免下一段因上游尚未提交而失败。
- 切分算法只按 rune 字数硬切，保证每段 rune 数不超过 `max_chars`，不会因为短 role 标记文本单独形成一段。
- 账号切换重试时也会在新 session 上重新走分段发送，保证切换后仍完整提交所有段。
- 分段发送返回的 `StartResult` 与 `StartCompletion` 完全一致，下游 `ExecuteNonStreamStartedWithRetry` / `ExecuteStreamWithRetry` 无缝接入。
- Vercel stream 链路（`__stream_prepare` / `__stream_switch`）同样接入分段：prepare 在 Go 侧先对前 N-1 段执行 `FireCompletionAndStop`，只把最后一段的 payload 交给 Node 层直连 DeepSeek；账号切换时会在新 session 上重新分段。
- 降级回退：分段链依赖上游"stop 后消息已提交到会话树"。若某段发送失败、或 stop 后未收到提交确认（未等到 `event: close` 且连接被超时强制关闭，`ErrSegmentCommitUnconfirmed`），继续以该段为 parent 续发会让上游无法把前序分段并入上下文，最终表现为"PRO 模型丢失上下文"。此时 runtime 回退为单消息发送：把剩余分段按序拼接还原原文（分段是 rune 硬切，拼接即原文），并以最后一个已确认提交的分段 id 作为 parent，保证最终请求携带尽可能完整的上下文而不是直接报错。回退会记录 `[expert_segment_fallback]` 告警日志，便于线上确认。

相关实现：

- 分段判断：
  [internal/completionruntime/prompt_segment.go](../internal/completionruntime/prompt_segment.go)
- 多段续发编排：
  [internal/completionruntime/segments.go](../internal/completionruntime/segments.go)
- 内容检测与停止：
  [internal/deepseek/client/client_stop_stream.go](../internal/deepseek/client/client_stop_stream.go)
- 字数切分算法：
  [internal/prompt/segment.go](../internal/prompt/segment.go)
- 配置访问器：
  [internal/config/store_accessors.go](../internal/config/store_accessors.go)

### 9.2 expert 模式文本文件内联（expert_text_file_inline）

expert（pro）模型本身不会收到任何 `ref_file_ids` 或 inline 文件引用。为了让文本类附件仍能被专家模型读取，兼容层在 expert 请求进入 prompt 构建前，会先把常见文本格式（`.txt`、`.md`、`.csv`、代码文件等）的内容从内存缓存中读出，替换为同位置的 `{"type":"text","text":"..."}` 块。这样文件内容直接成为 `FinalPrompt` 的一部分：

- 默认开启；仅对 `model_type == "expert"` 的模型生效。
- 非 expert 模型保持原行为：文件走 `ref_file_ids` 上传引用。
- 内联前会校验文件扩展名或 MIME 类型；非文本文件（图片、PDF 等）会被保留但 expert 模型最终仍不会收到它们。此时会记录 `[expert_attachment_dropped]` 告警日志（含文件名/MIME），便于线上确认"PRO 模型看不到附件"的原因是上游模型本身不支持非文本附件；若 `expert_text_file_inline` 被关闭，顶层文件引用同样会打该日志。
- 单个文件超过 `expert_text_file_inline.max_file_bytes`（默认 `3145728`，即 3 MiB）会直接拒绝请求（HTTP 413）。
- 用户可通过 `expert_text_file_inline.allowed_extensions` 自定义扩展名白名单；提供后默认 MIME 回退会被禁用，只按扩展名判断。
- 文件内容统一按 UTF-8 读取，非法字节替换为 `\ufffd`。
- 内联后文本会计入 `FinalPrompt` 长度，因此超长文本文件会自然触发 `expert_prompt_segment` 分段发送。
- 除 `messages`/`input`/`attachments` 中的 `input_file` 引用外，请求顶层 `file_ids` / `ref_file_ids` 中的文本文件同样会被内联到最后一个 user 消息（非文本引用仍会被 expert payload 丢弃）。
- 该处理在 OpenAI Chat、Responses 和 Vercel stream prepare 路径中统一执行，Vercel 续发/切换时复用已 prepared 的请求，无需再次处理。
- 注意：`MemoryContentStore` 是进程内内存缓存（默认 30 分钟 TTL）。在 Vercel 上文件上传（`POST /v1/files`）与后续 chat prepare 可能落到不同的 Go 实例，此时 prepare 会因缓存未命中返回 400 `text file content unavailable`。该限制在单实例自部署下不存在；如需多实例共享，可基于 `files.ContentStore` 接口自行实现共享存储（如 Redis）。

相关实现：

- 文本格式判断：
  [internal/httpapi/openai/files/text_format.go](../internal/httpapi/openai/files/text_format.go)
- 专家模型内联处理：
  [internal/httpapi/openai/files/expert_text_file_inline.go](../internal/httpapi/openai/files/expert_text_file_inline.go)
- 内存文件缓存：
  [internal/httpapi/openai/files/file_content_store.go](../internal/httpapi/openai/files/file_content_store.go)
- 上传时缓存文件内容：
  [internal/httpapi/openai/files/handler_files.go](../internal/httpapi/openai/files/handler_files.go)

## 10. 各协议入口的差异

### 10.1 OpenAI Chat / Responses

特点：

- `developer` 会映射到 `system`
- Responses `instructions` 会 prepend 为 system message
- 普通直传时 `tools` 会注入 system prompt；`current_input_file` 触发时工具描述/schema 会拆成 `TOOLS.txt`，system prompt 保留格式/策略规则并明确要求模型从 `TOOLS.txt` 获取可调用工具和 schema
- `attachments` / `input_file` / inline 文件会进入 `ref_file_ids`；对 expert 模型，文本类文件会按 [9.2](#92-expert-模式文本文件内联expert_text_file_inline) 内联进 prompt
- current input file 在统一 completion runtime 入口全局生效

### 10.2 Claude Messages

特点：

- top-level `system` 优先作为系统提示
- `tool_use` / `tool_result` 会被转换成统一的 assistant/tool 历史语义
- 普通直传时 `tools` 同样会被并进 system prompt；`current_input_file` 触发时会沿用统一的 `TOOLS.txt` 拆分上传路径
- 常规执行通过 `internal/httpapi/claude/handler_messages.go` 转到 OpenAI chat 路径，模型 alias 会先解析成 DeepSeek 原生模型
- 当前代码里没有像 OpenAI 那样完整的 `ref_file_ids` 附件链路

### 10.3 Gemini

特点：

- `systemInstruction`、`contents.parts`、`functionCall`、`functionResponse` 会先归一
- tools 会转成 OpenAI 风格 function schema
- prompt 构建复用 OpenAI 的 `promptcompat.BuildOpenAIPromptForAdapter`，`current_input_file` 触发时也会使用统一的 `TOOLS.txt` 拆分上传路径
- 未识别的非文本 part 会被安全序列化进 prompt，并对二进制/疑似 base64 内容做省略或截断处理

也就是说，Gemini 在“最终 prompt 语义”上，尽量和 OpenAI 保持一致。

## 11. 一份贴近真实的最终上下文示意

假设用户发来一个多轮请求：

- 有 system/developer 文本
- 有 tools
- 有一个文件型 systemprompt 附件
- 有历史 assistant tool call / tool result
- current input file 已触发

那么最终上下文更接近：

```json
{
  "prompt": "<System>:原 system / developer\n\n工具调用格式规范 — 请严格遵照执行： ...<User>:继续会话 使用工具时请参照说明与格式要求，仅使用所列出的工具<Assistant>:",
  "ref_file_ids": [
    "file-ds2api-history",
    "file-ds2api-tools",
    "file-systemprompt",
    "file-other-attachment"
  ],
  "thinking_enabled": true,
  "search_enabled": false
}
```

这正是“API 转网页对话纯文本”的核心成果：

- 大部分结构化语义被压进 `prompt`
- 文件保持文件
- 需要时把完整上下文拆进 `HISTORY.txt` 上下文文件，并按轮次编号成 transcript

## 12. 修改时必须同步本文档的场景

只要触碰以下任一类行为，就必须在同一提交或同一 PR 中更新本文档：

- 角色映射变更
- system / developer / instructions 合并规则变更
- assistant reasoning 保留格式变更
- assistant 历史 `tool_calls` 的 XML 呈现方式变更
- tool result 注入方式变更
- tool prompt 模板或 tool_choice 约束变更
- inline 文件上传 / 文件引用收集规则变更
- `auto_route_vision` 开关、路由目标模型、图片剔除范围变更
- current input file 触发条件、上传格式、`HISTORY.txt` transcript 结构变更
- expert 模式提示词分段（`expert_prompt_segment`）触发条件、切分算法或续发逻辑变更
- 旧 `history_split` 字段忽略/清理行为变更
- completion payload 字段语义变更
- Claude / Gemini 对这套统一语义的复用关系变更

优先检查这些文件：

- `internal/promptcompat/request_normalize.go`
- `internal/promptcompat/prompt_build.go`
- `internal/promptcompat/message_normalize.go`
- `internal/promptcompat/tool_prompt.go`
- `internal/httpapi/openai/files/file_inline_upload.go`
- `internal/promptcompat/file_refs.go`
- `internal/promptcompat/image_route.go`
- `internal/httpapi/openai/history/current_input_file.go`
- `internal/completionruntime/nonstream.go`
- `internal/promptcompat/responses_input_normalize.go`
- `internal/httpapi/claude/standard_request.go`
- `internal/httpapi/claude/handler_utils.go`
- `internal/httpapi/gemini/convert_request.go`
- `internal/httpapi/gemini/convert_messages.go`
- `internal/httpapi/gemini/convert_tools.go`
- `internal/prompt/messages.go`
- `internal/prompt/tool_calls.go`
- `internal/prompt/segment.go`
- `internal/promptcompat/standard_request.go`
- `internal/completionruntime/prompt_segment.go`
- `internal/completionruntime/segments.go`

## 13. 建议的最小验证

改动这条链路后，至少补齐或检查这些测试：

- `go test ./internal/prompt/...`
- `go test ./internal/httpapi/openai/...`
- `go test ./internal/httpapi/claude/...`
- `go test ./internal/httpapi/gemini/...`
- `go test ./internal/util/...`

如果改的是 tool call 相关兼容语义，还应同时检查：

- `go test ./internal/toolcall/...`
- `go test ./internal/toolstream/...`
- `./tests/scripts/run-unit-node.sh`

## 14. 文档同步约定

本文档是这条兼容链路的专项说明。

如果外部接口行为也变了，还应同步检查：

- [API.md](../API.md)
- [API.en.md](../API.en.md)
- [docs/toolcall-semantics.md](./toolcall-semantics.md)

原则是：

- 内部主链路变化，至少更新本文档
- 外部可见契约变化，再同步更新 API 文档
