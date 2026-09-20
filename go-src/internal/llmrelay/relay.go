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
	errKind       string // 错误归类：auth | rate_limit | upstream | connect | timeout | client（空=无错误）
	wroteToClient bool   // 是否已向客户端写出过实质数据（SSE 注释行 ping/notice 不算；决定能否换号重试）
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
			callbackAsync(app, lastDispatch, result, model, stream)
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
			callbackAsync(app, d, res, model, stream)
			return
		}
		// failed：可重试（未向客户端输出实质数据）且未达上限 → 换号重来
		if res.retryable && !res.wroteToClient && attempt < d.RetryPolicy.MaxRetries {
			tried = append(tried, d.AccountID)
			result = res
			continue
		}
		// 不可重试或已输出：若尚未写出任何内容则补发错误 chunk
		if !res.wroteToClient && flusher != nil {
			writeSSEError(w, flusher, res.errMessage)
		}
		callbackAsync(app, d, res, model, stream)
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
		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode >= 500
		return &streamResult{status: "failed",
			errMessage: fmt.Sprintf("上游返回 %d: %s", resp.StatusCode, msg),
			errKind:    statusErrKind(resp.StatusCode),
			retryable:  retryable}
	}

	if !stream {
		// 非流式：整体缓冲后一次性返回
		data, rerr := io.ReadAll(resp.Body)
		if rerr != nil {
			// 响应尚未写回客户端，可换号重试
			return &streamResult{status: "failed", errMessage: "读取上游响应失败: " + rerr.Error(), retryable: true}
		}
		// 非流式按 JSON 透传（Content-Type 沿用上游）
		for k, vs := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "content-") {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		// 非流式响应为完整 JSON，结构与 SSE chunk 兼容（choices[].finish_reason + usage）
		res := parseChunk(data)
		return &streamResult{
			status: "success", promptTokens: res.promptTokens,
			completionTok: res.completionTokens, totalTokens: res.totalTokens,
			finishReason: res.finishReason,
		}
	}

	// 流式转发（含心跳/空闲检测/usage 提取）
	res := streamForward(w, flusher, ctx, resp.Body, policy, start)
	if res.status == "" {
		res.status = "success"
	}
	return res
}

// streamForward 流式转发核心：goroutine 泵读上游，select 循环写客户端，
// 空离心跳、空闲超时、长任务预警、客户端中断响应
func streamForward(w http.ResponseWriter, flusher http.Flusher, ctx context.Context,
	src io.Reader, policy RetryPolicy, start time.Time) *streamResult {

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
	longTaskNotified := false
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// pending 行缓冲 + 输出闸门：chunk 可能跨行切断，按行缓冲后逐行过滤写出。
	// 首个实质内容 chunk 到达前（sawContent=false），数据行只缓冲不输出——
	// 上游若零输出即关闭，缓冲整体丢弃、未向客户端写出任何实质数据，
	// 可换号重试（对齐 PHP 通道「故障被当成成功」修复的无感切换语义）；
	// 闸门打开后缓冲行随当前流一并输出
	pending := make([]byte, 0, 64*1024)
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
			if _, err := w.Write(sanitizeSSELine(line)); err != nil {
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
						if _, werr := w.Write(sanitizeSSELine(pending)); werr != nil {
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
				// 零输出：行缓冲残留不输出（保持未写出实质数据状态）
				return &streamResult{status: "failed", wroteToClient: wrote, retryable: true,
					errMessage: "上游零输出（连接建立后未返回任何实质内容）", errKind: "upstream",
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
			// 长任务预警（event: notice，与 PHP 通道格式一致）
			if !longTaskNotified && policy.LongTaskThresholdMs > 0 &&
				time.Since(start) > msDuration(policy.LongTaskThresholdMs) {
				w.Write([]byte("event: notice\ndata: {\"type\":\"long_task\",\"message\":\"任务执行时间较长，请耐心等待\"}\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
				longTaskNotified = true
			}

		case <-ctx.Done():
			return &streamResult{status: "interrupted", wroteToClient: wrote, errKind: "client",
				errMessage:   "客户端中断",
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

	payload := map[string]interface{}{
		"model":             model,
		"stream":            stream,
		"client_ip":         clientIP,
		"tried_account_ids": tried,
		// 客户端原始请求体随决策请求上送：改写权在 CmsPro 侧
		// （模型别名解析等），改写结果经 body_override 下发
		"body": json.RawMessage(origBody),
	}
	payloadBytes, _ := json.Marshal(payload)

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

// callbackAsync 异步回调日志接口（成功/失败/中断均回调；失败仅记录日志）
func callbackAsync(app string, d *DispatchData, res *streamResult, model string, stream bool) {
	if d == nil || d.DispatchID == "" {
		return
	}
	go func() {
		cfg := config.Get()
		endpoint := strings.TrimRight(cfg.CmsproBaseURL, "/") + "/api/" + app + "/internal/log-callback"
		payload := map[string]interface{}{
			"dispatch_id":       d.DispatchID,
			"status":            res.status,
			"error_kind":        res.errKind,
			"stream":            stream,
			"model":             model,
			"account_id":        d.AccountID,
			"proxy_ip":          d.ProxyIP,
			"prompt_tokens":     res.promptTokens,
			"completion_tokens": res.completionTok,
			"total_tokens":      res.totalTokens,
			"error_message":     res.errMessage,
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
	}()
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
				if r := scanLine(s.buf); r.promptTokens != nil || r.completionTokens != nil {
					r.hasContent = r.hasContent || out.hasContent
					out = r
				} else if r.hasContent {
					out.hasContent = true
				}
				s.buf = nil
			}
			return out
		}
		line := s.buf[:idx]
		s.buf = s.buf[idx+1:]
		if r := scanLine(line); r.promptTokens != nil || r.completionTokens != nil {
			r.hasContent = r.hasContent || out.hasContent
			out = r
		} else if r.hasContent {
			out.hasContent = true
		}
	}
}

// scanLine 扫描单行 SSE 数据：粗筛 data 行后解析（usage 以最后一行为准）。
// hasContent 累计不覆盖（多行或运算），usage/finish_reason 保留最后值
func scanLine(line []byte) scanResult {
	// 粗筛：仅含 delta/finish_reason/usage 的数据行值得 JSON 解析
	// （[DONE]、注释行、event 行、错误对象行直接跳过）
	if !(bytes.Contains(line, []byte(`"delta"`)) ||
		bytes.Contains(line, []byte(`"finish_reason"`)) ||
		bytes.Contains(line, []byte(`"usage"`))) {
		return scanResult{}
	}
	data, ok := trimSSEData(line)
	if !ok {
		return scanResult{}
	}
	return parseChunk(data)
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
	data, ok := trimSSEData(line)
	if !ok || len(data) == 0 || data[0] != '{' {
		return line // 非 data 行 / [DONE] / 非对象载荷原样透传
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return line
	}
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

// parseChunk 解析 SSE chunk JSON，提取 usage / finish_reason / 实质内容标记
func parseChunk(data []byte) scanResult {
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
