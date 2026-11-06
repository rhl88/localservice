// Package llmrelay AI 通用透传处理器
//
// 提供 OpenAI 兼容接口的 SSE 流式透传能力：接收客户端请求后，
// 先调用对应 CmsPro 应用的「决策接口」获取上游地址/凭证/出口代理，
// 再以 goroutine 流式转发上游响应，流结束后回调「日志接口」落库。
//
// 路由约定（在 api/server.go 注册）：
//
//	POST/GET /open/llm/{app}/v1/*path   app 为应用对外 API 前缀名（如 cline）
//	GET      /open/llm/ping             健康检查
//
// 鉴权模型：本处理器不做应用级鉴权，客户端 Authorization 头原样透传给
// 决策接口，由 PHP 侧统一校验；9501 仅监听 127.0.0.1。
package llmrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cmspro-local-service/internal/config"
)

// ProxyConfig 出口代理配置（决策接口返回）
type ProxyConfig struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// RetryPolicy 重试与流控策略（决策接口返回，取自应用配置）
type RetryPolicy struct {
	MaxRetries           int   `json:"max_retries"`
	HeaderTimeoutMs      int64 `json:"header_timeout_ms"`
	FirstByteTimeoutMs   int64 `json:"first_byte_timeout_ms"`
	ReadTimeoutMs        int64 `json:"read_timeout_ms"`
	HeartbeatIntervalMs  int64 `json:"heartbeat_interval_ms"`
	FirstByteHeartbeatMs int64 `json:"first_byte_heartbeat_ms"`
	LongTaskThresholdMs  int64 `json:"long_task_threshold_ms"`
}

// LocalResponse PHP 决策的本地短路响应（models 等管理类端点由 CmsPro 侧直接出数，不透传上游）
type LocalResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// DispatchData 决策接口 data 载荷
type DispatchData struct {
	DispatchID    string            `json:"dispatch_id"`
	UpstreamURL   string            `json:"upstream_url"`
	AuthHeader    string            `json:"auth_header"`
	AuthExtra     map[string]string `json:"auth_extra"`
	Proxy         *ProxyConfig      `json:"proxy"`
	AccountID     int64             `json:"account_id"`
	ProxyIP       string            `json:"proxy_ip"`
	RetryPolicy   RetryPolicy       `json:"retry_policy"`
	LocalResponse *LocalResponse    `json:"local_response"`
	// BodyOverride 改写后的请求体（如模型别名解析为真实 model_id），
	// 非空时整体替换客户端原始请求体转发上游
	BodyOverride json.RawMessage `json:"body_override"`
	// ResponseTransform 响应转换指令（responses_to_chat：Responses SSE
	// 实时回转为 Chat Completions SSE），空=原样透传不做转换
	ResponseTransform string `json:"response_transform"`
	// ResponseTransformModel 响应转换时回写的模型名（chunk.model 字段），
	// PHP 决策下发解析后的真实 model_id
	ResponseTransformModel string `json:"response_transform_model"`
	// StripToolNames 需从响应中剔除的工具调用名（可选；缺省 nil/空=完全保持
	// 原行为，既有接入方零感知）。用于剔除网关注入的「入场券工具」被上游回显
	// 时产生的幽灵调用，避免客户端收到未声明的工具调用而误执行
	StripToolNames []string `json:"strip_tool_names"`
}

type dispatchResponse struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    *DispatchData `json:"data"`
}

// streamResult 单次转发的结果状态
type streamResult struct {
	status        string // success | failed | interrupted
	promptTokens  *int64
	completionTok *int64
	totalTokens   *int64
	finishReason  string
	errMessage    string
	errKind       string // 错误归类：auth | rate_limit | quota | upstream | connect | timeout | client（空=无错误）
	httpCode      int    // 上游 HTTP 语义状态码（含 SSE 流内错误事件的 code 字段），0=无
	wroteToClient bool   // 是否已向客户端写出过实质数据（SSE 注释行 ping 不算；决定能否换号重试）
	retryable     bool   // 失败是否可换号重试（连接层失败与上游状态码语义）
}

var (
	httpClientOnce sync.Once
	httpClient     *http.Client // 决策/回调专用（无代理，短超时）
)

// internalClient 决策/日志回调专用 HTTP 客户端
func internalClient() *http.Client {
	httpClientOnce.Do(func() {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	})
	return httpClient
}

// Handler 透传路由入口（无应用级鉴权）
func Handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/open/llm/ping" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"code": 0, "message": "llm relay ready", "data": nil,
		})
		return
	}

	if !strings.HasPrefix(r.URL.Path, "/open/llm/") {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"code": 404, "message": "未知路径", "data": nil,
		})
		return
	}

	// 解析 /open/llm/{app}/{path...}
	rest := strings.TrimPrefix(r.URL.Path, "/open/llm/")
	segs := strings.SplitN(rest, "/", 2)
	if len(segs) != 2 || segs[0] == "" || segs[1] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"code": 400, "message": "路径格式应为 /open/llm/{app}/{path}", "data": nil,
		})
		return
	}
	app, path := segs[0], segs[1]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"code": 400, "message": "读取请求体失败: " + err.Error(), "data": nil,
		})
		return
	}
	// 剥离 UTF-8 BOM：部分 Windows 客户端/脚本工具生成的请求体带 BOM 头，
	// 不剥离会导致模型解析失败，且决策请求构造时 json.Marshal 对含 BOM 的
	// RawMessage 校验不过，最终在决策端表现为误导性的「参数不能为空」
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})

	relay(w, r, app, path, body)
}

// relay 主流程：决策 → 转发 → 回调（含换号重试）
func relay(w http.ResponseWriter, r *http.Request, app, path string, body []byte) {
	// SSE 响应基础头（决策失败时也会以此格式返回错误，保证客户端可解析）
	setSSEHeaders(w)
	flusher, _ := w.(http.Flusher)

	authHeader := r.Header.Get("Authorization")
	model, stream := extractModelAndStream(body)
	clientIP := clientIP(r)
	origBody := body // 决策请求始终携带客户端原始请求体（改写只影响转发，不污染后续决策）

	var tried []int64
	var lastDispatch *DispatchData
	var result *streamResult

	for attempt := 0; ; attempt++ {
		d, derr := callDispatch(r.Context(), app, authHeader, path, model, stream, clientIP, tried, origBody)
		if derr != nil {
			// 决策层（PHP）已含选号/重试语义，失败直接以错误 chunk 返回
			result = &streamResult{status: "failed", errMessage: derr.Error()}
			if flusher != nil {
				writeSSEError(w, flusher, derr.Error())
			}
			callbackAsync(app, lastDispatch, result, model, stream, attempt+1)
			return
		}
		lastDispatch = d

		// 本地短路：决策接口指定本地响应（models 等管理类端点），不连上游、不回调日志
		if d.LocalResponse != nil && len(d.LocalResponse.Body) > 0 {
			status := d.LocalResponse.Status
			if status == 0 {
				status = http.StatusOK
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write(d.LocalResponse.Body)
			return
		}

		// 请求体改写：决策接口下发改写后的请求体（如模型别名解析为真实
		// model_id），整体替换客户端原始请求体后转发上游
		if len(d.BodyOverride) > 0 {
			body = []byte(d.BodyOverride)
		}

		res := forwardOnce(w, r, flusher, d, path, body, model, stream)
		if res.status == "success" || res.status == "interrupted" {
			callbackAsync(app, d, res, model, stream, attempt+1)
			return
		}
		// failed：可重试（未向客户端输出实质数据）且未达上限 → 换号重来
		if res.retryable && !res.wroteToClient && attempt < d.RetryPolicy.MaxRetries {
			tried = append(tried, d.AccountID)
			// 中间尝试逐次同步回调：PHP 侧即时归因（429 冷却 / 401 禁用 /
			// 出口失败计数），对齐 PHP 通道「每轮失败立即 markFailure」的
			// 调度健康度语义——后续请求不再撞刚失败的 Key/出口
			callbackSync(app, d, res, model, stream, attempt+1)
			// 重试节流：429 上游限流多为分钟级窗口配额（TPM/RPM），喘息前
			// 立即重试必然再撞同一窗口，对齐 PHP 通道固定喘息 1s；
			// 连接层失败（EOF/拒连）多为上游网关秒级~分钟级抖动窗口，立即
			// 重试全部撞在窗口内穷尽失败（实测 20s 窗口内 4 轮全灭），指数
			// 退避（2s/4s/8s）提高穿越概率，也降低高频建连触发上游风控的风险；
			// 退避期间客户端中断提前收尾（本次失败已回调，无需再回调）
			switch {
			case res.errKind == "rate_limit":
				select {
				case <-time.After(rateLimitBackoff):
				case <-r.Context().Done():
					return
				}
			case res.errKind == "connect":
				select {
				case <-time.After(connectBackoff(attempt)):
				case <-r.Context().Done():
					return
				}
			}
			continue
		}
		// 不可重试或已输出：若尚未写出任何内容则补发错误 chunk
		if !res.wroteToClient && flusher != nil {
			writeSSEError(w, flusher, res.errMessage)
		}
		callbackAsync(app, d, res, model, stream, attempt+1)
		return
	}
}

// forwardOnce 单轮转发：决策结果 → 上游请求 → SSE 流转发
func forwardOnce(w http.ResponseWriter, r *http.Request, flusher http.Flusher,
	d *DispatchData, path string, body []byte, model string, stream bool) *streamResult {

	start := time.Now()
	policy := d.RetryPolicy

	// 构造上游 Transport（可选出口代理）。
	// 等响应头预算：优先 header_timeout_ms（对齐 PHP 通道 header_timeout 配置，
	// 覆盖大上下文 prefill 的完整耗时），未下发（0/负）时兜底 first_byte_timeout_ms
	headerTimeoutMs := policy.HeaderTimeoutMs
	if headerTimeoutMs <= 0 {
		headerTimeoutMs = policy.FirstByteTimeoutMs
	}
	transport := &http.Transport{
		ResponseHeaderTimeout: msDuration(headerTimeoutMs),
	}
	if d.Proxy != nil && d.Proxy.Host != "" {
		proxyURL, err := buildProxyURL(d.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		} else {
			log.Printf("[llmrelay] 代理配置无效: %v", err)
		}
	}
	client := &http.Client{Transport: transport}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	method := r.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, d.UpstreamURL, bytes.NewReader(body))
	if err != nil {
		return &streamResult{status: "failed", errMessage: "构造上游请求失败: " + err.Error(), retryable: true}
	}
	req.Header.Set("Content-Type", "application/json")
	if d.AuthHeader != "" {
		req.Header.Set("Authorization", d.AuthHeader)
	}
	for k, v := range d.AuthExtra {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return &streamResult{status: "interrupted", errMessage: "客户端中断", errKind: "client"}
		}
		// 连接层失败（含首字节超时）：对齐 PHP 通道故障切换语义——
		// 未向客户端输出前换号重试（重新决策排除已试账号/换出口），而非直接终止
		return &streamResult{status: "failed", errMessage: "上游连接失败: " + err.Error(), errKind: "connect", retryable: true}
	}
	defer resp.Body.Close()

	// 非 200：提取错误信息，判定是否可换号重试
	if resp.StatusCode != http.StatusOK {
		msg := readErrorBody(resp)
		// 402（额度/积分不足）为单个 Key 的额度耗尽语义，与 429/401/403 同类：
		// 未向客户端输出前换号重试，多 Key 场景可平滑切换到仍有额度的 Key；
		// 不纳入重试集会直接终止（历史缺陷：多 Key 场景 402 直接停止）
		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusPaymentRequired ||
			resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode >= 500
		errMessage := fmt.Sprintf("上游返回 %d: %s", resp.StatusCode, msg)
		if resp.StatusCode == http.StatusTooManyRequests {
			// 对齐 PHP 通道 annotateRateLimitMessage：TPM/RPM 归类与恢复指引
			errMessage = annotateRateLimitMessage(errMessage)
		}
		return &streamResult{status: "failed",
			errMessage: errMessage,
			errKind:    statusErrKind(resp.StatusCode),
			httpCode:   resp.StatusCode,
			retryable:  retryable}
	}

	if !stream {
		// 非流式：整体缓冲后一次性返回
		data, rerr := io.ReadAll(resp.Body)
		if rerr != nil {
			// 响应尚未写回客户端，可换号重试
			return &streamResult{status: "failed", errMessage: "读取上游响应失败: " + rerr.Error(), retryable: true}
		}
		// 非流式响应统一为 JSON（覆盖 relay 入口预设的 SSE 头）
		w.Header().Set("Content-Type", "application/json")
		// chat 端点（协议转换）：上游 Responses SSE 回转为 Chat SSE 后聚合。
		// 客户端按非流式发起，原样透传 Responses 事件会导致其解析失败
		if d.ResponseTransform == responseTransformChat {
			return aggregateNonStream(w, convertResponsesSSEToChat(data, d.ResponseTransformModel), d.ResponseTransformModel, d.StripToolNames)
		}
		// 上游强制返回 SSE 流（如华为 DevEco 仅支持 stream=true）：聚合为标准
		// 非流式 chat.completion JSON——客户端按非流式发起，原样透传 SSE 会
		// 导致其 JSON 解析失败
		if isSSEPayload(resp.Header.Get("Content-Type"), data) {
			return aggregateNonStream(w, data, model, d.StripToolNames)
		}
		// 上游返回完整 JSON：按需剔除幽灵工具调用后原样透传
		w.WriteHeader(http.StatusOK)
		w.Write(filterToolCallsInJSON(data, d.StripToolNames))
		// 非流式响应为完整 JSON，结构与 SSE chunk 兼容（choices[].finish_reason + usage）
		res := parseChunk(data)
		return &streamResult{
			status: "success", promptTokens: res.promptTokens,
			completionTok: res.completionTokens, totalTokens: res.totalTokens,
			finishReason: res.finishReason,
			wroteToClient: true,
		}
	}

	// 流式转发（含心跳/空闲检测/usage 提取）。
	// chat 端点（协议转换）：上游 Responses SSE 实时回转为 Chat SSE，
	// 心跳/输出闸门/超时/零输出重试等流控机制全部复用
	src := io.Reader(resp.Body)
	if d.ResponseTransform == responseTransformChat {
		src = newResponsesToChatReader(resp.Body, d.ResponseTransformModel)
	}
	res := streamForward(w, flusher, ctx, src, policy, start, resp.Header.Get("Content-Type"), d.StripToolNames)
	if res.status == "" {
		res.status = "success"
	}
	return res
}

// aggregateNonStream 将上游 SSE 流聚合为标准非流式 chat.completion JSON 输出给
// 客户端，返回带 usage/finish_reason 的结果供日志回调落库。stripNames 非空时
// 按名剔除幽灵工具调用。聚合后无任何实质内容 → 判 failed 且可换号重试
// （对齐流式「零输出」的无感切换语义）
func aggregateNonStream(w http.ResponseWriter, data []byte, model string, stripNames []string) *streamResult {
	agg := &sseAggregator{model: model, toolCalls: make(map[int]*aggToolCall), strip: toolNameSet(stripNames)}
	agg.feed(data)
	return agg.writeTo(w)
}

// isSSEPayload 判定上游响应是否为 SSE 流：Content-Type 为 text/event-stream，
// 或载荷去除前导空白后以 SSE 字段（data:/id:）起始。上游返回完整 JSON 时
// （Content-Type application/json、以 { 起始）判定为否，走原样透传
func isSSEPayload(contentType string, data []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return true
	}
	head := bytes.TrimLeft(data, " \t\r\n")
	return bytes.HasPrefix(head, []byte("data:")) || bytes.HasPrefix(head, []byte("id:"))
}

// sseAggregator 非流式聚合器：把上游 Chat Completions SSE 增量合并为标准非流式
// 响应（移植对齐 DevecoService::aggregateSseMessage 语义：跳过 id: 行、只取每条
// 消息的最后一条 data: 行、忽略 [DONE]、忽略空 choices 的 usage chunk、累积
// content/reasoning_content、role 取首个、tool_calls 按 index 增量合并、
// finish_reason 非空覆盖、usage 取最后非空且 total 缺失时按 prompt+completion 补算）
type sseAggregator struct {
	model         string // 请求模型（上游未回显 model 时兜底）
	id            string // 上游首个 chunk 回显的 id
	upstreamModel string // 上游首个 chunk 回显的 model
	role          string
	content       strings.Builder
	reasoning     strings.Builder
	toolCalls     map[int]*aggToolCall
	toolOrder     []int // tool_calls 的 index 出现顺序（保持输出顺序稳定）
	usagePrompt   *int64
	usageComp     *int64
	usageTotal    *int64
	finishReason  string
	hasContent    bool
	// streamErr 聚合过程中见到的首个 SSE 流内错误事件（nil=无）
	streamErr *sseErrorPayload
	// strip 需剔除的工具调用名集合（nil=不过滤，保持原行为）
	strip map[string]struct{}
}

// aggToolCall 聚合后的单个工具调用（arguments 为增量拼接结果）
type aggToolCall struct {
	id        string
	typ       string
	name      string
	arguments strings.Builder
}

// aggToolCallDelta 单条 tool_calls 增量（index 定位、function 增量）
type aggToolCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function *struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// feed 按 SSE 消息边界（空行）切分并逐条聚合
func (a *sseAggregator) feed(raw []byte) {
	// 归一化换行，避免 CRLF/LF 混用导致消息边界漏切
	norm := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	norm = bytes.ReplaceAll(norm, []byte("\r"), []byte("\n"))
	for _, msg := range bytes.Split(norm, []byte("\n\n")) {
		if len(bytes.TrimSpace(msg)) > 0 {
			a.aggregateMessage(msg)
		}
	}
}

// aggregateMessage 取单条 SSE 消息的最后一条 data: 行并聚合
func (a *sseAggregator) aggregateMessage(msg []byte) {
	var dataLine []byte
	found := false
	for _, line := range bytes.Split(msg, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("id:")) {
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			dataLine = bytes.TrimSpace(line[len("data:"):])
			found = true
			// 流内错误事件不含任何实质内容，须单独识别，否则被聚合器
			// 当成普通 chunk 解析失败后忽略，最终误判为「上游返回空响应」
			if a.streamErr == nil {
				a.streamErr = parseSSEErrorData(dataLine)
			}
		}
	}
	if !found || len(dataLine) == 0 || string(dataLine) == "[DONE]" {
		return
	}
	a.aggregateData(dataLine)
}

// aggregateData 聚合单个 chunk 数据为累积状态
func (a *sseAggregator) aggregateData(data []byte) {
	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content          *string            `json:"content"`
				ReasoningContent *string            `json:"reasoning_content"`
				Role             *string            `json:"role"`
				ToolCalls        []aggToolCallDelta `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			TotalTokens      *int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return
	}

	// 首个 chunk 回显的 id / model（取到即固定）
	if a.id == "" && chunk.ID != "" {
		a.id = chunk.ID
	}
	if a.upstreamModel == "" && chunk.Model != "" {
		a.upstreamModel = chunk.Model
	}

	// usage 取最后一个非空（上游 usage 常在收尾的空 choices chunk 中出现）
	if chunk.Usage != nil {
		if chunk.Usage.PromptTokens != nil {
			a.usagePrompt = chunk.Usage.PromptTokens
		}
		if chunk.Usage.CompletionTokens != nil {
			a.usageComp = chunk.Usage.CompletionTokens
		}
		if chunk.Usage.TotalTokens != nil {
			a.usageTotal = chunk.Usage.TotalTokens
		}
	}

	// 空 choices 的 usage 专用 chunk：无内容可聚合
	if len(chunk.Choices) == 0 {
		return
	}
	choice := chunk.Choices[0]
	if choice.Delta.Content != nil && *choice.Delta.Content != "" {
		a.content.WriteString(*choice.Delta.Content)
		a.hasContent = true
	}
	if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
		a.reasoning.WriteString(*choice.Delta.ReasoningContent)
		a.hasContent = true
	}
	if a.role == "" && choice.Delta.Role != nil && *choice.Delta.Role != "" {
		a.role = *choice.Delta.Role
	}
	for _, tc := range choice.Delta.ToolCalls {
		a.mergeToolCall(tc)
		a.hasContent = true
	}
	if choice.FinishReason != nil && *choice.FinishReason != "" {
		a.finishReason = *choice.FinishReason
		a.hasContent = true
	}
}

// mergeToolCall 按 index 增量合并工具调用（name/arguments 字符串拼接）
func (a *sseAggregator) mergeToolCall(tc aggToolCallDelta) {
	idx := len(a.toolOrder)
	if tc.Index != nil {
		idx = *tc.Index
	}
	cur, ok := a.toolCalls[idx]
	if !ok {
		typ := tc.Type
		if typ == "" {
			typ = "function"
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", idx)
		}
		cur = &aggToolCall{id: id, typ: typ}
		a.toolCalls[idx] = cur
		a.toolOrder = append(a.toolOrder, idx)
	}
	if tc.ID != "" {
		cur.id = tc.ID
	}
	if tc.Function != nil {
		cur.name += tc.Function.Name
		cur.arguments.WriteString(tc.Function.Arguments)
	}
}

// writeTo 输出标准非流式响应；无实质内容时返回 failed（可换号重试）
func (a *sseAggregator) writeTo(w http.ResponseWriter) *streamResult {
	if !a.hasContent {
		// 流内错误事件优先于「空响应」：携带上游真实 code/type/message，
		// 使 PHP 侧能正确归类冷却；此时响应尚未写回客户端，可换号重试
		if a.streamErr != nil {
			return streamErrorResult(a.streamErr, false)
		}
		return &streamResult{status: "failed", errMessage: "上游返回空响应", retryable: true}
	}
	role := a.role
	if role == "" {
		role = "assistant"
	}
	outModel := a.upstreamModel
	if outModel == "" {
		outModel = a.model
	}
	finishReason := a.finishReason
	if finishReason == "" {
		finishReason = "stop"
	}
	id := a.id
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}

	message := map[string]interface{}{
		"role":    role,
		"content": a.content.String(),
	}
	if a.reasoning.Len() > 0 {
		message["reasoning_content"] = a.reasoning.String()
	}
	calls := make([]map[string]interface{}, 0, len(a.toolOrder))
	for _, idx := range a.toolOrder {
		c := a.toolCalls[idx]
		// 按名单剔除幽灵工具调用（如入场券工具被上游回显）
		if a.strip != nil {
			if _, drop := a.strip[c.name]; drop {
				continue
			}
		}
		calls = append(calls, map[string]interface{}{
			"id":   c.id,
			"type": c.typ,
			"function": map[string]interface{}{
				"name":      c.name,
				"arguments": c.arguments.String(),
			},
		})
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	// 幽灵调用被全部剔除且无正文/思考内容：等价于上游零输出，判 failed
	// 触发换号重试，避免仅回显入场券工具的无效响应被记成成功
	if len(calls) == 0 && a.content.Len() == 0 && a.reasoning.Len() == 0 {
		return &streamResult{status: "failed", errMessage: "上游返回空响应（工具调用已按名单剔除）", retryable: true}
	}

	p, c, t := a.tokenUsage()
	resp := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   outModel,
		"choices": []map[string]interface{}{
			{"index": 0, "message": message, "finish_reason": finishReason},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     p,
			"completion_tokens": c,
			"total_tokens":      t,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)

	return &streamResult{
		status:        "success",
		promptTokens:  int64Ptr(p),
		completionTok: int64Ptr(c),
		totalTokens:   int64Ptr(t),
		finishReason:  finishReason,
		wroteToClient: true,
	}
}

// tokenUsage 汇总 token 用量（total 缺失时按 prompt+completion 补算）
func (a *sseAggregator) tokenUsage() (int64, int64, int64) {
	var p, c int64
	if a.usagePrompt != nil {
		p = *a.usagePrompt
	}
	if a.usageComp != nil {
		c = *a.usageComp
	}
	t := p + c
	if a.usageTotal != nil {
		t = *a.usageTotal
	}
	return p, c, t
}

// int64Ptr 取 int64 值指针（供 streamResult 的 token 字段使用）
func int64Ptr(v int64) *int64 {
	return &v
}

// zeroOutputDiag 组装「上游零输出」诊断串：附加上游 Content-Type 与已收到的
// 原始响应片段，用于区分「上游真空流」与「上游返回非 SSE 格式被误判」。
// 仅作证据留存，不参与任何判定或重试决策。
func zeroOutputDiag(contentType string, raw []byte) string {
	if contentType == "" {
		contentType = "(未提供)"
	}
	const maxSnippet = 200
	snippet := raw
	if len(snippet) > maxSnippet {
		snippet = snippet[:maxSnippet]
	}
	text := strings.ToValidUTF8(string(snippet), "")
	text = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text)
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(空)"
	}
	return fmt.Sprintf(" [诊断: 上游Content-Type=%s, 已收原文=%s]", contentType, text)
}

// sseErrorPayload SSE 流内错误事件载荷（data: {"error":{...}}）
type sseErrorPayload struct {
	Message string // 上游错误文案
	Type    string // 上游错误类型（如 ModelServiceRateLimit）
	Code    int    // 上游错误码（HTTP 语义，如 403），0=未提供
}

// parseSSEErrorLine 解析 SSE 数据行中的流内错误事件。
//
// 华为 DevEco 等上游以 HTTP 200 + `event: error` + `data: {"error":{...}}`
// 表达账号级故障（403 ModelServiceRateLimit / 429 等），若不在扫描阶段识别，
// 该行会被粗筛当成「无实质内容的普通行」跳过，最终误判为「上游零输出」且
// errKind 归 upstream（不冷却账号、无喘息退避、日志文案无信息量）。
//
// code 字段上游可能返回字符串（"403"）或数字，需兼容；非 data 行、
// `"error": null`、非错误载荷一律返回 nil（正常 chunk 不受影响）。
func parseSSEErrorLine(line []byte) *sseErrorPayload {
	if !bytes.Contains(line, []byte(`"error"`)) {
		return nil // 粗筛：绝大多数正常数据行不含该键，避免无谓 JSON 解析
	}
	data, ok := trimSSEData(line)
	if !ok {
		return nil
	}
	return parseSSEErrorData(data)
}

// parseSSEErrorData 解析已剥离 data: 前缀的 SSE 数据载荷中的错误对象
// （非流式聚合路径复用，其输入已是纯 JSON 字节）
func parseSSEErrorData(data []byte) *sseErrorPayload {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || !bytes.Contains(data, []byte(`"error"`)) {
		return nil
	}
	var probe struct {
		Error *struct {
			Message string      `json:"message"`
			Type    string      `json:"type"`
			Code    interface{} `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &probe); err != nil || probe.Error == nil {
		return nil
	}
	return &sseErrorPayload{
		Message: strings.TrimSpace(probe.Error.Message),
		Type:    strings.TrimSpace(probe.Error.Type),
		Code:    toStatusCode(probe.Error.Code),
	}
}

// toStatusCode 把错误载荷的 code 字段归一为 HTTP 语义状态码
// （华为返回字符串 "403"，OpenAI 系多为数字或 null）
func toStatusCode(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	}
	return 0
}

// streamErrKind 流内错误事件的 errKind 归类。
//
// 优先按上游错误类型语义归类：华为 ModelServiceRateLimit 以 code=403 表达
// 服务级限流，若按 HTTP 字面归 auth 会冷却 300s（配额型故障实际分钟级恢复），
// 且 4 账号池下极易耗尽可用账号返回 503。类型无法判定时回退状态码语义。
func streamErrKind(code int, errType string) string {
	t := strings.ToLower(errType)
	if strings.Contains(t, "ratelimit") || strings.Contains(t, "rate_limit") {
		return "rate_limit"
	}
	return statusErrKind(code)
}

// streamErrMessage 组装流内错误的日志文案：前缀携带状态码与错误类型，
// 使调用日志列表（错误列截断显示前 40 字）内即可定位故障性质
func streamErrMessage(e *sseErrorPayload) string {
	msg := e.Message
	if msg == "" {
		msg = "上游未提供错误详情"
	}
	var tag string
	switch {
	case e.Code > 0 && e.Type != "":
		tag = fmt.Sprintf("%d %s", e.Code, e.Type)
	case e.Code > 0:
		tag = strconv.Itoa(e.Code)
	default:
		tag = e.Type
	}
	if tag == "" {
		return "上游流内错误: " + msg
	}
	return fmt.Sprintf("上游流内错误[%s]: %s", tag, msg)
}

// streamErrorResult 由 SSE 流内错误事件构造失败结果。
//
// wroteToClient 为真（输出闸门已开、实质内容已写出）时无法换号，按失败归因
// 收尾；为假时标记可重试，主循环换号重来，客户端对故障无感。
func streamErrorResult(e *sseErrorPayload, wroteToClient bool) *streamResult {
	kind := streamErrKind(e.Code, e.Type)
	msg := streamErrMessage(e)
	if kind == "rate_limit" {
		msg = annotateRateLimitMessage(msg)
	}
	return &streamResult{
		status: "failed", wroteToClient: wroteToClient, retryable: !wroteToClient,
		errMessage: msg, errKind: kind, httpCode: e.Code,
	}
}

// streamForward 流式转发核心：goroutine 泵读上游，select 循环写客户端，
// 空离心跳、空闲超时、长任务预警、客户端中断响应
func streamForward(w http.ResponseWriter, flusher http.Flusher, ctx context.Context,
	src io.Reader, policy RetryPolicy, start time.Time, upstreamContentType string, stripNames []string) *streamResult {

	dataCh := make(chan []byte, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(dataCh)
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				dataCh <- chunk
			}
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	sc := &lineScanner{} // 跨块行缓冲，扫描 usage / finish_reason
	// headersAt：响应头已收到（进入本函数即 client.Do 成功返回）。首数据字节
	// 前的空闲计时从此刻起算（对齐 PHP 通道 first_byte_timeout「响应头之后
	// 的空读上限」语义，prefill 期间不再受等头预算双重挤压）
	headersAt := time.Now()
	activity := headersAt // 上游最近一次数据到达时间（仅上游数据更新，心跳不计入）
	lastPing := start     // 上次心跳写出时间（独立计时，不掩蔽空闲超时）
	firstByte := false
	wrote := false      // 是否已向客户端写出过数据
	sawContent := false // 是否见过实质内容 chunk（content/reasoning/tool_calls/finish_reason）
	sawDone := false    // 是否已把流结束标记 [DONE] 写出给客户端（流语义已完整结束）
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// pending 行缓冲 + 输出闸门：chunk 可能跨行切断，按行缓冲后逐行过滤写出。
	// 首个实质内容 chunk 到达前（sawContent=false），数据行只缓冲不输出——
	// 上游若零输出即关闭，缓冲整体丢弃、未向客户端写出任何实质数据，
	// 可换号重试（对齐 PHP 通道「故障被当成成功」修复的无感切换语义）；
	// 闸门打开后缓冲行随当前流一并输出
	pending := make([]byte, 0, 64*1024)
	// SSE 行清洗器：strip 名单为空时等价原 sanitizeSSELine（既有接入方零
	// 行为变更）；有名单时额外按 index 跟踪工具调用名并剔除幽灵调用
	cleaner := newStreamSanitizer(stripNames)
	emit := func(chunk []byte, content bool) error {
		pending = append(pending, chunk...)
		for {
			idx := bytes.IndexByte(pending, '\n')
			if idx < 0 {
				return nil
			}
			if !content && !sawContent {
				return nil // 闸门未开：保留行待实质内容到达后一并输出
			}
			line := pending[:idx+1] // 含行尾换行
			pending = pending[idx+1:]
			if _, err := w.Write(cleaner(line)); err != nil {
				return err
			}
		}
	}

	var lastResult scanResult

	for {
		select {
		case chunk, ok := <-dataCh:
			if !ok {
				// 上游关闭：EOF 视为正常结束——但零输出视为上游故障
				// （对齐 PHP 通道「故障被当成成功」修复：整条流无任何
				// 实质内容 chunk 时判失败，避免调用日志把故障记成成功）；
				// 未向客户端输出实质数据时缓冲整体丢弃，可换号重试
				if err := <-errCh; err != nil && err != io.EOF {
					// 未输出实质数据时标记可重试（主循环按 wroteToClient 拦截已输出场景）
					return &streamResult{status: "failed", wroteToClient: wrote, retryable: true,
						errMessage: "上游读取失败: " + err.Error(), errKind: "upstream"}
				}
				if sawContent {
					// 处理行缓冲残留（最后一行无换行结尾）
					if len(pending) > 0 {
						if _, werr := w.Write(cleaner(pending)); werr != nil {
							return &streamResult{status: "interrupted", wroteToClient: wrote, errKind: "client",
								errMessage: "客户端写入失败: " + werr.Error()}
						}
						pending = nil
					}
					if r := sc.flush(); r.promptTokens != nil || r.completionTokens != nil {
						lastResult = r
					}
					return &streamResult{
						status: "success", promptTokens: lastResult.promptTokens,
						completionTok: lastResult.completionTokens, totalTokens: lastResult.totalTokens,
						finishReason: lastResult.finishReason,
					}
				}
				// 行缓冲可能残留最后一行（上游未以换行收尾）：补扫一次，避免
				// 流内错误事件被漏判为「上游零输出」而丢失真实归因与换号能力
				if fr := sc.flush(); fr.streamErr != nil {
					return streamErrorResult(fr.streamErr, wrote)
				}
				// 零输出：行缓冲残留不输出（保持未写出实质数据状态）。pending 此刻
				// 保留上游全部原始响应字节（闸门未开时不消费），作为诊断证据随日志落库
				return &streamResult{status: "failed", wroteToClient: wrote, retryable: true,
					errMessage: "上游零输出（连接建立后未返回任何实质内容）" +
						zeroOutputDiag(upstreamContentType, pending), errKind: "upstream",
					promptTokens: lastResult.promptTokens, completionTok: lastResult.completionTokens,
					totalTokens: lastResult.totalTokens, finishReason: lastResult.finishReason}
			}
			activity = time.Now()
			firstByte = true
			r := sc.feed(chunk)
			if r.promptTokens != nil || r.completionTokens != nil {
				lastResult = r
			}
			if r.hasContent {
				sawContent = true
			}
			if err := emit(chunk, r.hasContent); err != nil {
				return &streamResult{status: "interrupted", wroteToClient: true, errKind: "client",
					errMessage: "客户端写入失败: " + err.Error()}
			}
			if sawContent {
				wrote = true
				if flusher != nil {
					flusher.Flush()
				}
			}
			// 上游流内错误事件（HTTP 200 + SSE event:error）：闸门未开时上方
			// emit 只缓冲未写出，可无感换号重试；闸门已开则错误行已随流写给
			// 客户端，无法换号，按失败归因（不再误记成功）
			if r.streamErr != nil {
				return streamErrorResult(r.streamErr, sawContent)
			}
			// [DONE] 已写出客户端：流语义完整结束。置于 emit 成功之后，
			// 使该标志严格等价于「客户端已完整收到响应」
			if r.done {
				sawDone = true
			}

		case <-ticker.C:
			// 空闲超时（对齐 PHP 通道超时语义）：仅上游数据到达才重置空闲计时，
			// 心跳注释不掩蔽半开连接检测；未输出实质数据时标记可重试。
			// 首数据字节前（大上下文 prefill 期）：上限取 first_byte_timeout 与
			// read_timeout 的较小值（0/负=该项禁用）；首字节后维持 read_timeout
			if firstByte {
				if policy.ReadTimeoutMs > 0 && time.Since(activity) > msDuration(policy.ReadTimeoutMs) {
					return &streamResult{status: "failed", wroteToClient: wrote, retryable: true,
						errMessage: "上游空闲超时（半开连接判定）", errKind: "timeout",
						promptTokens: lastResult.promptTokens, completionTok: lastResult.completionTokens,
						totalTokens: lastResult.totalTokens, finishReason: lastResult.finishReason}
				}
			} else if limit := minPositiveMs(policy.FirstByteTimeoutMs, policy.ReadTimeoutMs); limit > 0 &&
				time.Since(headersAt) > msDuration(limit) {
				return &streamResult{status: "failed", wroteToClient: wrote, retryable: true,
					errMessage: "上游首数据超时（响应头已收到但未产出数据，prefill 可能过长）", errKind: "timeout",
					promptTokens: lastResult.promptTokens, completionTok: lastResult.completionTokens,
					totalTokens: lastResult.totalTokens, finishReason: lastResult.finishReason}
			}
			// 心跳：首字节前用更短间隔
			interval := msDuration(policy.HeartbeatIntervalMs)
			if !firstByte && policy.FirstByteHeartbeatMs > 0 {
				interval = msDuration(policy.FirstByteHeartbeatMs)
			}
			if interval > 0 && time.Since(lastPing) >= interval {
				w.Write([]byte(": ping\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
				lastPing = time.Now()
			}
			// 不发 event: notice 长任务预警：data JSON 非标准 OpenAI chunk（无 choices/error），
			// Cline 等严格客户端 zod 校验失败会直接断流；保活由上方 : ping 注释行承担

		case <-ctx.Done():
			// 已把 [DONE] 完整写出给客户端后连接才断开：这是客户端收到流结束
			// 标记即关闭连接的正常收尾，不是中断。此时上游 FIN 往往还在公网
			// RTT 途中、dataCh 未关闭，select 三路只能命中本分支；若判为
			// interrupted，已完整送达的成功响应就会被记成「客户端中断」。
			// 连接已关闭，不再补写行缓冲残留，直接按成功归档
			if sawDone && sawContent {
				if r := sc.flush(); r.promptTokens != nil || r.completionTokens != nil {
					lastResult = r
				}
				return &streamResult{
					status: "success", wroteToClient: wrote, promptTokens: lastResult.promptTokens,
					completionTok: lastResult.completionTokens, totalTokens: lastResult.totalTokens,
					finishReason: lastResult.finishReason,
				}
			}
			// 中断文案携带输出时长与已产出 tokens：中断日志多为客户端取消且
			// 占比高，同文案无区分度，需逐条点详情才能判断中断时机与损失
			outMsg := "客户端中断"
			elapsed := time.Since(start).Round(time.Second)
			if lastResult.completionTokens != nil {
				outMsg = fmt.Sprintf("客户端中断（输出 %s 后取消，已产出约 %d tokens）", elapsed, *lastResult.completionTokens)
			} else if sawContent {
				outMsg = fmt.Sprintf("客户端中断（输出 %s 后取消）", elapsed)
			}
			return &streamResult{status: "interrupted", wroteToClient: wrote, errKind: "client",
				errMessage:   outMsg,
				promptTokens: lastResult.promptTokens, completionTok: lastResult.completionTokens,
				totalTokens: lastResult.totalTokens, finishReason: lastResult.finishReason}
		}
	}
}

// ---------- 决策与回调 ----------

// callDispatch 调用应用决策接口
func callDispatch(ctx context.Context, app, authHeader, path string, model string,
	stream bool, clientIP string, tried []int64, origBody []byte) (*DispatchData, error) {

	cfg := config.Get()
	endpoint := strings.TrimRight(cfg.CmsproBaseURL, "/") + "/api/" + app + "/internal/dispatch"

	// GET 等无 body 请求以 JSON null 占位：RawMessage(空) 序列化会报
	// unexpected end of JSON input，导致 models 等管理类端点决策请求
	// 构造失败；非空 body 保持原样（非法 JSON 仍显式报错，不静默吞掉）
	var bodyField json.RawMessage = json.RawMessage("null")
	if len(origBody) > 0 {
		bodyField = json.RawMessage(origBody)
	}
	payload := map[string]interface{}{
		"model":             model,
		"stream":            stream,
		"client_ip":         clientIP,
		"tried_account_ids": tried,
		// 客户端原始请求体随决策请求上送：改写权在 CmsPro 侧
		// （模型别名解析等），改写结果经 body_override 下发
		"body": bodyField,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		// 客户端原始请求体不是合法 JSON（RawMessage 嵌入校验失败）：
		// 若忽略错误，决策请求会以空 body 发出，决策端只能报出误导性的
		// 「messages 参数不能为空」；此处显式报错便于客户端定位问题
		return nil, fmt.Errorf("客户端请求体不是合法 JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("构造决策请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cmspro-Dispatch-Path", path)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := internalClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("决策接口不可达: %w", err)
	}
	defer resp.Body.Close()

	var dr dispatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, fmt.Errorf("决策接口响应解析失败 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || dr.Code != 0 || dr.Data == nil {
		msg := dr.Message
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return dr.Data, nil
}

// callbackSync 同步回调日志接口：阻塞至 PHP 侧处理完成（租约消费/Key 冷却
// 生效）。用于换号重试前的中间尝试——确保失败归因先于下一次决策生效，
// 逐次落日志并携带尝试轮次
func callbackSync(app string, d *DispatchData, res *streamResult, model string, stream bool, attempt int) {
	doCallback(app, d, res, model, stream, attempt)
}

// callbackAsync 异步回调日志接口（最终结果收尾不阻塞客户端响应）
func callbackAsync(app string, d *DispatchData, res *streamResult, model string, stream bool, attempt int) {
	go doCallback(app, d, res, model, stream, attempt)
}

// doCallback 回调日志接口（成功/失败/中断均回调；失败仅记录日志）。
// attempt 为本次尝试轮次（1=首轮，>1=换号重试的中间尝试），随日志落库
// 用于区分一次客户端请求在日志列表产生的多条失败记录
func doCallback(app string, d *DispatchData, res *streamResult, model string, stream bool, attempt int) {
	if d == nil || d.DispatchID == "" {
		return
	}
	cfg := config.Get()
	endpoint := strings.TrimRight(cfg.CmsproBaseURL, "/") + "/api/" + app + "/internal/log-callback"
	payload := map[string]interface{}{
		"dispatch_id":       d.DispatchID,
		"status":            res.status,
		"error_kind":        res.errKind,
		"attempt":           attempt,
		"stream":            stream,
		"model":             model,
		"account_id":        d.AccountID,
		"proxy_ip":          d.ProxyIP,
		"prompt_tokens":     res.promptTokens,
		"completion_tokens": res.completionTok,
		"total_tokens":      res.totalTokens,
		"error_message":     res.errMessage,
	}
	// 上游 HTTP 语义状态码（含 SSE 流内错误的 code）：PHP 侧据此落库日志
	// http_code，使调用日志页可按状态码筛查；未取到时不上报（PHP 侧留空）
	if res.httpCode > 0 {
		payload["http_code"] = res.httpCode
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		log.Printf("[llmrelay] 构造回调请求失败: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := internalClient().Do(req)
	if err != nil {
		log.Printf("[llmrelay] 日志回调失败 (dispatch=%s): %v", d.DispatchID, err)
		return
	}
	resp.Body.Close()
}

// rateLimitBackoff 429 换号重试前的喘息间隔（对齐 PHP 通道 429 切 Key 前
// 固定喘息 1s；包级变量便于测试缩短等待）
var rateLimitBackoff = time.Second

// connectBackoff 连接层失败的换号重试指数退避（attempt 从 0 起）：
// 2s/4s/8s 封顶，配合 max_retries=3 总退避 14s，可穿越秒级~20s 级上游抖动窗口
func connectBackoff(attempt int) time.Duration {
	d := 2 * time.Second << attempt
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

// annotateRateLimitMessage 429 限流消息语义化注解（对齐 PHP 通道
// ProxyService::annotateRateLimitMessage 文案）：按上游错误码归类为
// 账号分钟级配额超限，标注恢复预期与处置建议；未识别的 429 变体
// 原样返回，避免对未知形态附加误导性说明
func annotateRateLimitMessage(msg string) string {
	var reason string
	switch {
	case strings.Contains(msg, "ModelAccountTpmRateLimitExceeded"), strings.Contains(msg, "tpm exhausted"):
		reason = "账号每分钟 Token 配额（TPM）超限"
	case strings.Contains(msg, "ModelAccountRpmRateLimitExceeded"), strings.Contains(msg, "rpm exhausted"):
		reason = "账号每分钟请求数（RPM）超限"
	case strings.Contains(msg, "ModelServiceRateLimit"):
		// 华为 DevEco 以 SSE 流内错误事件（code=403）下发的服务级限流
		reason = "上游模型服务资源紧张（服务级限流）"
	case strings.Contains(msg, "insufficient_quota"), strings.Contains(msg, "tpm/rpm limit"):
		reason = "账号分钟级配额限流"
	default:
		return msg
	}
	return msg + "（" + reason + "：Key 已自动冷却，到点自动恢复；大上下文请求易触发，" +
		"频繁出现请扩充 Key 池或将模型分流到其他 Provider）"
}

// ---------- 辅助 ----------

// lineScanner 跨数据块的按行扫描器，提取 usage 与 finish_reason
type lineScanner struct {
	buf []byte
}

type scanResult struct {
	promptTokens     *int64
	completionTokens *int64
	totalTokens      *int64
	finishReason     string
	hasContent       bool // 本批次是否见过实质内容（content/reasoning/tool_calls/finish_reason）
	done             bool // 本批次是否见过 SSE 流结束标记 data: [DONE]
	streamErr        *sseErrorPayload // 本批次见到的首个 SSE 流内错误事件（nil=无）
}

func (s *lineScanner) feed(chunk []byte) scanResult {
	s.buf = append(s.buf, chunk...)
	return s.consume(false)
}

func (s *lineScanner) flush() scanResult {
	return s.consume(true)
}

func (s *lineScanner) consume(final bool) scanResult {
	var out scanResult
	for {
		idx := bytes.IndexByte(s.buf, '\n')
		if idx < 0 {
			if final && len(s.buf) > 0 {
				mergeScanResult(&out, scanLine(s.buf))
				s.buf = nil
			}
			return out
		}
		line := s.buf[:idx]
		s.buf = s.buf[idx+1:]
		mergeScanResult(&out, scanLine(line))
	}
}

// mergeScanResult 把单行扫描结果并入累计结果：
// usage / finish_reason 以最后一行为准（整条覆盖），hasContent / done 只累积不回退，
// streamErr 保留最早出现的一个（首个错误即故障根因）
func mergeScanResult(out *scanResult, r scanResult) {
	if r.promptTokens != nil || r.completionTokens != nil {
		r.hasContent = r.hasContent || out.hasContent
		r.done = r.done || out.done
		if r.streamErr == nil {
			r.streamErr = out.streamErr
		}
		*out = r
		return
	}
	out.hasContent = out.hasContent || r.hasContent
	out.done = out.done || r.done
	if out.streamErr == nil {
		out.streamErr = r.streamErr
	}
}

// scanLine 扫描单行 SSE 数据：粗筛 data 行后解析（usage 以最后一行为准）。
// hasContent 累计不覆盖（多行或运算），usage/finish_reason 保留最后值
func scanLine(line []byte) scanResult {
	// 流结束标记 data: [DONE] 需在粗筛前单独识别：它是流语义完成的唯一凭据，
	// 缺失时只能等上游 TCP FIN，客户端先断开就会被误判为中断
	if isSSEDone(line) {
		return scanResult{done: true}
	}
	// 流内错误事件需在粗筛前识别：错误行不含任何实质内容 needle，
	// 被粗筛拦截会导致账号级故障（403/429）误判为「上游零输出」
	if e := parseSSEErrorLine(line); e != nil {
		return scanResult{streamErr: e}
	}
	// 粗筛：仅含 delta/finish_reason/usage 的数据行值得 JSON 解析
	// （注释行、event 行直接跳过）。
	// Responses API 事件（"type":"response.*"）与整包对象（"object":"response"）
	// 亦需放行：response.output_item.added 等事件不含上述 needle，
	// 若被粗筛拦截将导致实质内容（工具调用）漏判为「上游零输出」
	if !(bytes.Contains(line, []byte(`"delta"`)) ||
		bytes.Contains(line, []byte(`"finish_reason"`)) ||
		bytes.Contains(line, []byte(`"usage"`)) ||
		bytes.Contains(line, []byte(`"response.`)) ||
		bytes.Contains(line, []byte(`"object":"response`)) ||
		bytes.Contains(line, []byte(`"object": "response`))) {
		return scanResult{}
	}
	data, ok := trimSSEData(line)
	if !ok {
		return scanResult{}
	}
	return parseChunk(data)
}

// sseDoneMarker OpenAI SSE 流结束标记载荷
var sseDoneMarker = []byte("[DONE]")

// isSSEDone 判定是否为流结束标记行 data: [DONE]
func isSSEDone(line []byte) bool {
	data, ok := trimSSEData(line)
	return ok && bytes.Equal(data, sseDoneMarker)
}

// trimSSEData 剥离 "data: " 前缀
func trimSSEData(line []byte) ([]byte, bool) {
	t := bytes.TrimSpace(line)
	if !bytes.HasPrefix(t, []byte("data:")) {
		return nil, false
	}
	return bytes.TrimSpace(t[5:]), true
}

// sseStrippedTopFields SSE 数据行的顶层非标准字段（对齐 PHP 通道 sanitizeChunk）。
// delta.reasoning / delta.reasoning_details（思考过程）不过滤，透传给支持
// 思考字段的客户端（Cherry Studio、DeepSeek 系等）
var sseStrippedTopFields = []string{"provider", "native_finish_reason", "service_tier"}

// sanitizeSSELine 对 SSE 数据行剔除非标准字段（对齐 PHP 通道 sanitizeChunk：
// 顶层 provider/native_finish_reason/service_tier 与 choices 内
// native_finish_reason），避免严格按 OpenAI 规范解析的客户端（Cline SDK /
// Rust serde 等）报 error decoding response body。
// 仅当行实际含非标准字段时才经 map 重组输出，否则原样返回——原始字节透传
// 保证 delta 空对象与字段顺序完全保真；重组时 Go 空 map 序列化为 {}，
// 同样保真空对象（无 PHP 关联数组解码陷阱）
func sanitizeSSELine(line []byte) []byte {
	return sanitizeLine(line, nil)
}

// sanitizeLine SSE 数据行清洗共享实现：剔除非标准字段，并在传入有状态清洗器
// 时按 tool_call 名剔除幽灵调用。仅当行实际有改动时才经 map 重组输出，否则
// 原样返回——原始字节透传保证 delta 空对象与字段顺序完全保真
func sanitizeLine(line []byte, s *streamSanitizer) []byte {
	data, ok := trimSSEData(line)
	if !ok || len(data) == 0 || data[0] != '{' {
		return line // 非 data 行 / [DONE] / 非对象载荷原样透传
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return line
	}
	changed := stripNonStandardFields(m)
	if s != nil {
		// 工具调用剔除需无条件执行（含状态副作用），故置于 || 左侧
		changed = s.stripToolCalls(m) || changed
	}
	if !changed {
		return line
	}
	nb, err := json.Marshal(m)
	if err != nil {
		return line
	}
	out := make([]byte, 0, len(nb)+8)
	out = append(out, []byte("data: ")...)
	out = append(out, nb...)
	out = append(out, '\n')
	return out
}

// stripNonStandardFields 剔除 SSE chunk 中的非标准字段（顶层
// provider/native_finish_reason/service_tier 与 choices 内
// native_finish_reason），避免严格按 OpenAI 规范解析的客户端
// （Cline SDK / Rust serde 等）报 error decoding response body；返回是否有改动
func stripNonStandardFields(m map[string]interface{}) bool {
	changed := false
	for _, k := range sseStrippedTopFields {
		if _, exists := m[k]; exists {
			delete(m, k)
			changed = true
		}
	}
	if choices, ok := m["choices"].([]interface{}); ok {
		for _, c := range choices {
			if cm, ok := c.(map[string]interface{}); ok {
				if _, exists := cm["native_finish_reason"]; exists {
					delete(cm, "native_finish_reason")
					changed = true
				}
			}
		}
	}
	return changed
}

// streamSanitizer SSE 数据行有状态清洗器：在字段剔除基础上，按 tool_call 的
// index 跟踪其 function.name，剔除命中名单的幽灵调用增量（如网关注入的入场券
// 工具被上游回显）。仅当 strip 非空时启用，缺省不参与任何过滤
type streamSanitizer struct {
	strip    map[string]struct{} // 需剔除的工具名集合
	accum    map[int]string      // 各 index 已累积的 function.name（按分片拼接）
	dropped  map[int]bool        // 各 index 已判定剔除
	released map[int]bool        // 各 index 已判定保留（此后不再检查）
}

// newStreamSanitizer 构造 SSE 行清洗函数：strip 名单为空时返回无状态清洗
// （完全等价历史行为，既有接入方零感知）；非空时返回带 index 跟踪状态者
func newStreamSanitizer(stripNames []string) func([]byte) []byte {
	set := toolNameSet(stripNames)
	if set == nil {
		return sanitizeSSELine
	}
	s := &streamSanitizer{
		strip:    set,
		accum:    make(map[int]string),
		dropped:  make(map[int]bool),
		released: make(map[int]bool),
	}
	return s.sanitize
}

// sanitize 有状态清洗入口
func (s *streamSanitizer) sanitize(line []byte) []byte {
	return sanitizeLine(line, s)
}

// stripToolCalls 按 index 跟踪工具名并从 choices[].delta.tool_calls 中剔除
// 命中名单的增量条目，返回是否有改动。名分片时按累积值判定——标准 OpenAI
// 兼容上游在首个增量即给出完整 function.name，一次判定即完成
func (s *streamSanitizer) stripToolCalls(m map[string]interface{}) bool {
	choices, ok := m["choices"].([]interface{})
	if !ok {
		return false
	}
	changed := false
	for _, c := range choices {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		delta, ok := cm["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		tcs, ok := delta["tool_calls"].([]interface{})
		if !ok {
			continue
		}
		kept := make([]interface{}, 0, len(tcs))
		for i, t := range tcs {
			tm, ok := t.(map[string]interface{})
			if !ok {
				kept = append(kept, t)
				continue
			}
			idx := toolCallIndex(tm, i)
			if s.dropped[idx] {
				changed = true
				continue
			}
			if !s.released[idx] {
				if name := toolCallName(tm); name != "" {
					s.accum[idx] += name
					switch {
					case s.hitsStrip(s.accum[idx]):
						s.dropped[idx] = true
						changed = true
						continue
					case !isPrefixOfAny(s.accum[idx], s.strip):
						// 已不可能成长为名单项（含以其为前缀的正常工具名）
						s.released[idx] = true
					}
				}
			}
			kept = append(kept, t)
		}
		switch {
		case len(tcs) > 0 && len(kept) == 0:
			// 全部被剔除：移除空数组，避免客户端收到无意义增量
			delete(delta, "tool_calls")
			changed = true
		case len(kept) != len(tcs):
			delta["tool_calls"] = kept
		}
	}
	return changed
}

// hitsStrip 判定工具名是否命中剔除名单
func (s *streamSanitizer) hitsStrip(name string) bool {
	_, ok := s.strip[name]
	return ok
}

// toolCallIndex 取 tool_call 增量声明的 index（缺省退回数组下标）
func toolCallIndex(tm map[string]interface{}, fallback int) int {
	if v, ok := tm["index"].(float64); ok {
		return int(v)
	}
	return fallback
}

// toolCallName 取 tool_call 增量的 function.name（缺省空串）
func toolCallName(tm map[string]interface{}) string {
	fn, ok := tm["function"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := fn["name"].(string)
	return name
}

// isPrefixOfAny 判定 name 是否为名单中某项的前缀（含完全相等）：用于识别仍在
// 分片累积中的工具名，避免过早放行导致残缺工具调用送达客户端
func isPrefixOfAny(name string, set map[string]struct{}) bool {
	for n := range set {
		if strings.HasPrefix(n, name) {
			return true
		}
	}
	return false
}

// toolNameSet 将工具名列表规整为查找集合并跳过空串；结果为空时返回 nil
// （nil=禁用工具调用过滤，保持历史行为）
func toolNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n != "" {
			set[n] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// filterToolCallsInJSON 非流式完整 JSON 响应中按名剔除幽灵工具调用
// （choices[].message.tool_calls）；名单为空或解析失败时原样返回
func filterToolCallsInJSON(data []byte, stripNames []string) []byte {
	set := toolNameSet(stripNames)
	if set == nil {
		return data
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return data
	}
	changed := false
	if choices, ok := m["choices"].([]interface{}); ok {
		for _, c := range choices {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			msg, ok := cm["message"].(map[string]interface{})
			if !ok {
				continue
			}
			tcs, ok := msg["tool_calls"].([]interface{})
			if !ok {
				continue
			}
			kept := make([]interface{}, 0, len(tcs))
			for _, t := range tcs {
				tm, ok := t.(map[string]interface{})
				if !ok {
					kept = append(kept, t)
					continue
				}
				if _, drop := set[toolCallName(tm)]; drop {
					changed = true
					continue
				}
				kept = append(kept, t)
			}
			if len(kept) == 0 {
				delete(msg, "tool_calls")
			} else {
				msg["tool_calls"] = kept
			}
		}
	}
	if !changed {
		return data
	}
	nb, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return nb
}

// parseChunk 解析 SSE chunk JSON，提取 usage / finish_reason / 实质内容标记。
// 先按 Responses API 形态解析（事件与整包），未命中再回退 Chat Completions
func parseChunk(data []byte) scanResult {
	if r, ok := parseResponsesChunk(data); ok {
		return r
	}
	var out scanResult
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          interface{} `json:"content"`
				ReasoningContent interface{} `json:"reasoning_content"`
				Reasoning        interface{} `json:"reasoning"`
				ToolCalls        interface{} `json:"tool_calls"`
				FunctionCall     interface{} `json:"function_call"`
			} `json:"delta"`
			FinishReason interface{} `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			TotalTokens      *int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return out
	}
	if chunk.Usage != nil {
		out.promptTokens = chunk.Usage.PromptTokens
		out.completionTokens = chunk.Usage.CompletionTokens
		out.totalTokens = chunk.Usage.TotalTokens
	}
	for _, c := range chunk.Choices {
		if meaningful(c.Delta.Content) || meaningful(c.Delta.ReasoningContent) ||
			meaningful(c.Delta.Reasoning) || meaningful(c.Delta.ToolCalls) ||
			meaningful(c.Delta.FunctionCall) || meaningful(c.FinishReason) {
			out.hasContent = true
			break
		}
	}
	if len(chunk.Choices) > 0 {
		switch v := chunk.Choices[0].FinishReason.(type) {
		case string:
			out.finishReason = v
		}
	}
	return out
}

// meaningful 判定 JSON 值是否为实质内容：nil/空串/空数组/空对象为否，
// 其余（非空串、非空数组/对象、数字、布尔）为是
func meaningful(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case []interface{}:
		return len(t) > 0
	case map[string]interface{}:
		return len(t) > 0
	default:
		return true
	}
}

// responsesUsageProbe Responses API token 用量字段（与 Chat Completions 命名不同）
type responsesUsageProbe struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
}

// responsesEventProbe Responses API 事件 / 整包对象的探测结构，仅取判定与统计所需字段
type responsesEventProbe struct {
	Type   string        `json:"type"`
	Object string        `json:"object"`
	Delta  interface{}   `json:"delta"`
	Text   interface{}   `json:"text"`
	Output []interface{} `json:"output"`
	Item   *struct {
		Type string `json:"type"`
	} `json:"item"`
	Response *struct {
		Output []interface{}        `json:"output"`
		Usage  *responsesUsageProbe `json:"usage"`
	} `json:"response"`
	Usage *responsesUsageProbe `json:"usage"`
}

// parseResponsesChunk 解析 Responses API 事件（SSE）或整包对象（非流式）。
// 返回 (结果, 是否命中)：未命中时调用方回退 Chat Completions 解析。
// 该 API 以 type=response.* 事件流表达，data 载荷无 choices 字段，
// 若按 Chat Completions 解析会漏判实质内容，进而被误判为「上游零输出」丢弃
func parseResponsesChunk(data []byte) (scanResult, bool) {
	var p responsesEventProbe
	if err := json.Unmarshal(data, &p); err != nil {
		return scanResult{}, false
	}
	if !strings.HasPrefix(p.Type, "response.") && p.Object != "response" {
		return scanResult{}, false
	}
	var out scanResult
	// usage：流式位于 response.completed 的 response.usage，整包位于顶层 usage
	usage := p.Usage
	if usage == nil && p.Response != nil {
		usage = p.Response.Usage
	}
	if usage != nil {
		out.promptTokens = usage.InputTokens
		out.completionTokens = usage.OutputTokens
		out.totalTokens = usage.TotalTokens
	}
	out.hasContent = responsesHasContent(&p)
	if p.Type == "response.completed" || p.Type == "response.incomplete" {
		out.done = true
	}
	if p.Type == "response.completed" {
		out.finishReason = "stop"
	}
	return out, true
}

// responsesHasContent 判定 Responses 事件是否携带实质内容：
// 非空增量（*.delta）、工具调用项的增删（item.type 以 _call 结尾）、
// 文本 / 推理完成态、完成事件内非空的 output 数组
func responsesHasContent(p *responsesEventProbe) bool {
	switch {
	case strings.HasSuffix(p.Type, ".delta"):
		return meaningful(p.Delta)
	case p.Type == "response.output_item.added" || p.Type == "response.output_item.done":
		return p.Item != nil && strings.HasSuffix(p.Item.Type, "_call")
	case p.Type == "response.output_text.done" || p.Type == "response.reasoning_text.done" ||
		p.Type == "response.reasoning_summary_text.done":
		return meaningful(p.Text)
	case p.Type == "response.completed":
		if p.Response != nil && len(p.Response.Output) > 0 {
			return true
		}
		return len(p.Output) > 0
	}
	return false
}

// extractModelAndStream 从请求体提取 model 与 stream 标记
func extractModelAndStream(body []byte) (string, bool) {
	var probe struct {
		Model  string `json:"model"`
		Stream *bool  `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", false
	}
	stream := false
	if probe.Stream != nil {
		stream = *probe.Stream
	}
	return probe.Model, stream
}

// clientIP 提取客户端 IP
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Real-IP"); fwd != "" {
		return fwd
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

// buildProxyURL 构造出口代理 URL（http/socks5）
func buildProxyURL(p *ProxyConfig) (*url.URL, error) {
	scheme := p.Type
	if scheme == "" {
		scheme = "http"
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", p.Host, p.Port),
	}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u, nil
}

// statusErrKind 将上游状态码归类为错误类型（供 PHP 侧账号/出口维护判定）
func statusErrKind(code int) string {
	switch {
	case code == http.StatusTooManyRequests:
		return "rate_limit"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "auth"
	case code == http.StatusPaymentRequired:
		return "quota"
	case code >= 500:
		return "upstream"
	default:
		return "upstream"
	}
}

// readErrorBody 读取上游错误响应体（限 64KB），尽量提取 error.message
func readErrorBody(resp *http.Response) string {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var probe struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &probe); err == nil {
		if probe.Error.Message != "" {
			return probe.Error.Message
		}
		if probe.Message != "" {
			return probe.Message
		}
	}
	t := strings.TrimSpace(string(data))
	if len(t) > 300 {
		t = t[:300] + "..."
	}
	return t
}

// setSSEHeaders 设置 SSE 响应头
func setSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
}

// writeSSEError 以 SSE error chunk 形式返回错误（保证严格客户端可解析）。
// 格式对齐 PHP 通道 formatSseError：错误事件同时携带 choices 字段（空 delta
// + finish_reason stop）——部分严格按 OpenAI 规范反序列化的客户端（Cline SDK /
// Rust serde 等）解析每个 data: 行时要求 choices 存在，仅有 error 会解码失败
// （error decoding response body）
func writeSSEError(w http.ResponseWriter, flusher http.Flusher, msg string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    "api_error",
			"param":   nil,
			"code":    nil,
		},
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]interface{}{}, "finish_reason": "stop"},
		},
	})
	fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// writeJSON 输出 JSON 响应
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// msDuration 毫秒转 time.Duration（0 或负值返回 0）
func msDuration(ms int64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// minPositiveMs 取两值中较小的正值（0/负视为该项禁用，不参与比较）；
// 两者均禁用时返回 0（调用方以 >0 判定是否启用上限）
func minPositiveMs(a, b int64) int64 {
	switch {
	case a > 0 && b > 0:
		if a < b {
			return a
		}
		return b
	case a > 0:
		return a
	case b > 0:
		return b
	default:
		return 0
	}
}
