// 本文件实现决策指令 response_transform=responses_to_chat 的响应方向协议转换：
// 上游 Responses API SSE → Chat Completions SSE。
//
// 背景：Codex 上游只提供 Responses API，Go 通道 chat/completions 端点由 PHP
// 决策层完成请求方向转换（body_override 下发 Responses 载荷，恒为流式），Go 侧
// 按本指令把上游 Responses 事件流实时回转为 Chat chunk 流，Chat 客户端同享
// Go 通道的稳定性（goroutine 流式转发，不占用 php-cgi worker）。
//
// 行为规格移植自 PHP 通道 ProtocolConverter（事件映射、chunk 结构、usage
// 归一化、首包兜底、EOF 补齐），保证两通道对客户端呈现一致的协议细节。
package llmrelay

import (
	"bytes"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// responseTransformChat 决策可下发的响应转换指令：Responses SSE → Chat
// Completions SSE（空值表示原样透传，不做转换）
const responseTransformChat = "responses_to_chat"

// chatChunkIDPrefix Chat chunk id 前缀（对齐 PHP 通道
// ProtocolConverter::CHUNK_ID_PREFIX，保证客户端可聚合）
const chatChunkIDPrefix = "chatcmpl-"

// responsesToChatConverter Responses 事件 → Chat chunk 转换器。
// 持有单次转发尝试的状态（responseId/model/toolIndex 等），每次转发尝试
// 须新建实例，换号重试的状态天然隔离（对齐 PHP 通道「一次请求一个实例」）
type responsesToChatConverter struct {
	displayModel string // 回写模型名（决策下发的真实 model_id）
	model        string // chunk.model 实际输出（displayModel 优先）
	responseID   string // 上游响应 id（chunk id 稳定基准）
	created      int64  // 上游响应创建时间戳
	sentFirst    bool   // 是否已产出角色首包
	sentFinish   bool   // 是否已产出结束 chunk（EOF 兜底判定依据）
	hasToolCall  bool   // 是否出现过工具调用
	toolIndex    int    // 当前工具调用序号（-1 表示尚未出现）
	finishReason string
	errorMessage string
	usage        map[string]interface{} // Chat 格式 usage（nil=未获取）
}

// newResponsesToChatConverter 新建转换器实例（禁止跨请求复用）
func newResponsesToChatConverter(displayModel string) *responsesToChatConverter {
	return &responsesToChatConverter{
		displayModel: displayModel,
		toolIndex:    -1,
		finishReason: "stop",
	}
}

// convertEvent 转换单个 Responses 事件为 0~n 个 Chat chunk（完整 SSE 字节）。
// 结束事件（completed/incomplete/failed）产出的结束 chunk 后追加 [DONE]
// 流结束标记——Responses 上游不发送该标记，而 Chat 客户端依赖它判定流结束
func (c *responsesToChatConverter) convertEvent(raw []byte) []byte {
	var ev map[string]interface{}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil
	}
	eventType, _ := ev["type"].(string)
	c.accumulate(ev, eventType)

	var chunks []map[string]interface{}
	finishInBatch := false
	switch eventType {
	case "response.created":
		// 首包必须带 role，否则部分客户端不渲染
		chunks = append(chunks, c.buildChunk(roleDelta()))
		c.sentFirst = true
	case "response.output_item.added":
		if item, ok := ev["item"].(map[string]interface{}); ok && item["type"] == "function_call" {
			chunks = append(chunks, c.buildToolCallStartChunk(item))
		}
	case "response.output_text.delta":
		if delta := jsonString(ev["delta"]); delta != "" {
			chunks = append(chunks, c.buildChunk(map[string]interface{}{"content": delta}))
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		// 推理内容统一映射到 reasoning_content（DeepSeek 系客户端约定）
		if delta := jsonString(ev["delta"]); delta != "" {
			chunks = append(chunks, c.buildChunk(map[string]interface{}{"reasoning_content": delta}))
		}
	case "response.function_call_arguments.delta":
		if delta := jsonString(ev["delta"]); delta != "" {
			chunks = append(chunks, c.buildToolCallDeltaChunk(delta))
		}
	case "response.failed":
		chunks = append(chunks, c.failedChunks()...)
		finishInBatch = true
	case "response.completed", "response.incomplete":
		chunks = append(chunks, c.buildFinishChunk())
		finishInBatch = true
	}
	return c.emitChunks(chunks, finishInBatch)
}

// finishStream 上游 EOF 兜底：流中断（未收到 completed/incomplete/failed）时
// 补发结束 chunk 与 [DONE]，保证客户端收到完整流语义（对齐 PHP 通道
// completeStreamOutput 的补齐行为）；已产出过结束 chunk 时返回空
func (c *responsesToChatConverter) finishStream() []byte {
	if c.sentFinish {
		return nil
	}
	return c.emitChunks([]map[string]interface{}{c.buildFinishChunk()}, true)
}

// accumulate 累积上游事件到转换状态（对齐 PHP 通道
// ProtocolConverter::accumulate 的事件级状态维护）
func (c *responsesToChatConverter) accumulate(ev map[string]interface{}, eventType string) {
	switch eventType {
	case "response.created":
		resp, _ := ev["response"].(map[string]interface{})
		c.responseID = jsonString(resp["id"])
		c.created = jsonInt64(resp["created_at"])
		if c.created == 0 {
			c.created = time.Now().Unix()
		}
		if c.displayModel != "" {
			c.model = c.displayModel
		} else {
			c.model = jsonString(resp["model"])
		}
	case "response.output_item.added":
		if item, ok := ev["item"].(map[string]interface{}); ok && item["type"] == "function_call" {
			c.hasToolCall = true
			c.toolIndex++
		}
	case "response.completed", "response.incomplete":
		c.accumulateCompletion(ev, eventType)
	case "response.failed":
		resp, _ := ev["response"].(map[string]interface{})
		errObj, _ := resp["error"].(map[string]interface{})
		c.errorMessage = jsonString(errObj["message"])
		if c.errorMessage == "" {
			c.errorMessage = "上游响应失败"
		}
		c.finishReason = "stop"
	}
}

// accumulateCompletion 结束事件处理：usage 归一化与结束原因推导
func (c *responsesToChatConverter) accumulateCompletion(ev map[string]interface{}, eventType string) {
	resp, _ := ev["response"].(map[string]interface{})
	if usage, ok := resp["usage"].(map[string]interface{}); ok {
		c.usage = mapChatUsage(usage)
	}
	if eventType == "response.incomplete" {
		c.finishReason = "length"
		return
	}
	if c.hasToolCall || outputHasFunctionCall(resp["output"]) {
		c.finishReason = "tool_calls"
	} else {
		c.finishReason = "stop"
	}
}

// failedChunks 上游显式失败事件：错误信息以 content 增量透出后发结束 chunk
func (c *responsesToChatConverter) failedChunks() []map[string]interface{} {
	var chunks []map[string]interface{}
	if c.errorMessage != "" {
		chunks = append(chunks, c.buildChunk(
			map[string]interface{}{"content": "\n[上游错误] " + c.errorMessage}))
	}
	return append(chunks, c.buildFinishChunk())
}

// emitChunks 兜底补角色首包（上游缺 response.created 时）并把 chunk 编码为
// SSE 字节；finishInBatch 表示本批含结束 chunk，需追加 [DONE] 流结束标记
func (c *responsesToChatConverter) emitChunks(chunks []map[string]interface{}, finishInBatch bool) []byte {
	if len(chunks) == 0 {
		return nil
	}
	if !c.sentFirst {
		chunks = append([]map[string]interface{}{c.buildChunk(roleDelta())}, chunks...)
		c.sentFirst = true
	}
	var buf bytes.Buffer
	for _, chunk := range chunks {
		data, err := json.Marshal(chunk)
		if err != nil {
			continue
		}
		buf.WriteString("data: ")
		buf.Write(data)
		buf.WriteString("\n\n")
	}
	if finishInBatch {
		buf.WriteString("data: [DONE]\n\n")
	}
	return buf.Bytes()
}

// roleDelta 角色首包 delta
func roleDelta() map[string]interface{} {
	return map[string]interface{}{"role": "assistant", "content": ""}
}

// buildToolCallStartChunk function_call 首个 chunk：带 id 与 name，
// 后续只发 arguments 增量
func (c *responsesToChatConverter) buildToolCallStartChunk(item map[string]interface{}) map[string]interface{} {
	callID := jsonString(item["call_id"])
	if callID == "" {
		callID = jsonString(item["id"])
	}
	delta := map[string]interface{}{
		"tool_calls": []interface{}{map[string]interface{}{
			"index": c.toolIndexClamped(),
			"id":    callID,
			"type":  "function",
			"function": map[string]interface{}{
				"name":      jsonString(item["name"]),
				"arguments": "",
			},
		}},
	}
	return c.buildChunk(delta)
}

// buildToolCallDeltaChunk 工具调用参数增量 chunk
func (c *responsesToChatConverter) buildToolCallDeltaChunk(delta string) map[string]interface{} {
	return c.buildChunk(map[string]interface{}{
		"tool_calls": []interface{}{map[string]interface{}{
			"index":    c.toolIndexClamped(),
			"function": map[string]interface{}{"arguments": delta},
		}},
	})
}

// buildChunk 构造普通增量 chunk
func (c *responsesToChatConverter) buildChunk(delta map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":      c.chunkID(),
		"object":  "chat.completion.chunk",
		"created": c.created,
		"model":   c.model,
		"choices": []interface{}{map[string]interface{}{
			"index":         0,
			"delta":         delta,
			"finish_reason": nil,
		}},
	}
}

// buildFinishChunk 构造结束 chunk（含 finish_reason 与 usage）。
// delta 必须输出 {}，部分客户端据此判定流结束
func (c *responsesToChatConverter) buildFinishChunk() map[string]interface{} {
	chunk := map[string]interface{}{
		"id":      c.chunkID(),
		"object":  "chat.completion.chunk",
		"created": c.created,
		"model":   c.model,
		"choices": []interface{}{map[string]interface{}{
			"index":         0,
			"delta":         map[string]interface{}{},
			"finish_reason": c.finishReason,
		}},
	}
	if c.usage != nil {
		chunk["usage"] = c.usage
	}
	c.sentFinish = true
	return chunk
}

// chunkID 构造稳定的 chunk id（基于上游响应 id，保证客户端可聚合）
func (c *responsesToChatConverter) chunkID() string {
	if c.responseID == "" {
		c.responseID = "resp_" + randomHex(8)
	}
	return chatChunkIDPrefix + c.responseID
}

// toolIndexClamped 工具调用下标（-1 表示尚未出现时钳位为 0）
func (c *responsesToChatConverter) toolIndexClamped() int {
	if c.toolIndex < 0 {
		return 0
	}
	return c.toolIndex
}

// mapChatUsage 归一化 usage：Responses 的 input/output_tokens → Chat 的
// prompt/completion_tokens，total 缺失时补算；明细字段（cached_tokens/
// reasoning_tokens）存在时映射（对齐 PHP 通道 ProtocolConverter::mapUsage）
func mapChatUsage(usage map[string]interface{}) map[string]interface{} {
	if len(usage) == 0 {
		return nil
	}
	promptTokens := jsonInt64(usage["input_tokens"])
	completionTokens := jsonInt64(usage["output_tokens"])
	totalTokens := jsonInt64(usage["total_tokens"])
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	mapped := map[string]interface{}{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
	if details, ok := usage["input_tokens_details"].(map[string]interface{}); ok {
		if _, exists := details["cached_tokens"]; exists {
			mapped["prompt_tokens_details"] = map[string]interface{}{
				"cached_tokens": jsonInt64(details["cached_tokens"]),
			}
		}
	}
	if details, ok := usage["output_tokens_details"].(map[string]interface{}); ok {
		if _, exists := details["reasoning_tokens"]; exists {
			mapped["completion_tokens_details"] = map[string]interface{}{
				"reasoning_tokens": jsonInt64(details["reasoning_tokens"]),
			}
		}
	}
	return mapped
}

// outputHasFunctionCall 最终 output 中是否包含工具调用
func outputHasFunctionCall(v interface{}) bool {
	output, ok := v.([]interface{})
	if !ok {
		return false
	}
	for _, item := range output {
		if m, ok := item.(map[string]interface{}); ok && m["type"] == "function_call" {
			return true
		}
	}
	return false
}

// ---------- io.Reader 流式包装 ----------

// responsesToChatReader 包装上游 Responses SSE 流为 Chat Completions SSE 流。
// 跨 Read 行缓冲，逐行把 data 事件经转换器转为 chat chunk 行；上游 EOF 且
// 未见结束事件时补发结束 chunk 与 [DONE]。注释行（: ping）与空行不透传，
// 转换后的流仅含 chat chunk 与 [DONE]——下游 lineScanner/心跳/输出闸门/
// 零输出重试等流控机制全部复用，无需感知转换的存在
type responsesToChatReader struct {
	src       io.Reader
	converter *responsesToChatConverter
	readBuf   []byte // 底层读缓冲（复用）
	lineBuf   []byte // 上游行缓冲（跨 Read 累积）
	outBuf    []byte // 转换产物（待读出）
	srcEOF    bool   // 上游已 EOF
	srcErr    error  // 底层读错误（产物吐尽后如实透传，不吞错）
	flushed   bool   // EOF 兜底产物已生成
}

// newResponsesToChatReader 新建流式转换 Reader
func newResponsesToChatReader(src io.Reader, displayModel string) *responsesToChatReader {
	return &responsesToChatReader{
		src:       src,
		converter: newResponsesToChatConverter(displayModel),
		readBuf:   make([]byte, 32*1024),
	}
}

// Read 实现 io.Reader：优先读出已转换产物，产物耗尽时继续泵入上游数据
func (r *responsesToChatReader) Read(p []byte) (int, error) {
	for {
		if len(r.outBuf) > 0 {
			n := copy(p, r.outBuf)
			r.outBuf = r.outBuf[n:]
			return n, nil
		}
		if r.srcEOF {
			if r.flushed {
				return 0, io.EOF
			}
			// 上游 EOF：转换器兜底补结束 chunk 与 [DONE]（已产出时为空）
			r.outBuf = r.converter.finishStream()
			r.flushed = true
			continue
		}
		if r.srcErr != nil {
			err := r.srcErr
			r.srcErr = nil
			return 0, err
		}
		r.pump()
	}
}

// pump 读一块上游数据并按行转换，EOF/错误记入状态
func (r *responsesToChatReader) pump() {
	n, err := r.src.Read(r.readBuf)
	if n > 0 {
		r.lineBuf = append(r.lineBuf, r.readBuf[:n]...)
		r.drainLines(false)
	}
	if err == nil {
		return
	}
	if err == io.EOF {
		r.drainLines(true) // 无换行结尾的残留行也参与转换
		r.srcEOF = true
		return
	}
	r.srcErr = err
}

// drainLines 消费行缓冲中的完整行（final=true 时把无换行结尾的残留末行一并转换）
func (r *responsesToChatReader) drainLines(final bool) {
	for {
		idx := bytes.IndexByte(r.lineBuf, '\n')
		if idx < 0 {
			if final && len(r.lineBuf) > 0 {
				r.convertLine(bytes.TrimSpace(r.lineBuf))
				r.lineBuf = nil
			}
			return
		}
		r.convertLine(bytes.TrimSpace(r.lineBuf[:idx]))
		r.lineBuf = r.lineBuf[idx+1:]
	}
}

// convertLine 转换单行上游 SSE：仅处理 data 事件行，注释/event 行丢弃
func (r *responsesToChatReader) convertLine(line []byte) {
	data, ok := trimSSEData(line)
	if !ok {
		return
	}
	if out := r.converter.convertEvent(data); len(out) > 0 {
		r.outBuf = append(r.outBuf, out...)
	}
}

// ---------- 整块转换（非流式响应路径） ----------

// convertResponsesSSEToChat 整块转换：上游 Responses SSE 字节 → Chat SSE
// 字节。非流式 chat 客户端由决策层强制上游流式（body_override stream=true），
// 全量转换后交 sseAggregator 聚合为 chat.completion JSON
func convertResponsesSSEToChat(data []byte, displayModel string) []byte {
	converter := newResponsesToChatConverter(displayModel)
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		payload, ok := trimSSEData(bytes.TrimSpace(line))
		if !ok {
			continue
		}
		if chunkOut := converter.convertEvent(payload); len(chunkOut) > 0 {
			out.Write(chunkOut)
		}
	}
	if tail := converter.finishStream(); len(tail) > 0 {
		out.Write(tail)
	}
	return out.Bytes()
}

// ---------- 辅助 ----------

// jsonString 安全提取 JSON 字符串值（非字符串返回空）
func jsonString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// jsonInt64 安全提取 JSON 整数值（number → int64，其余返回 0）
func jsonInt64(v interface{}) int64 {
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return int64(f)
}

// randomHex 生成 n 字节的十六进制随机串（chunk id 随机段兜底）
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
