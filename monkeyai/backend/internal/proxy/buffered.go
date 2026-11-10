package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// bufferChatResponse 将完整的上游结果转换成标准 SSE，保留工具调用和真实用量。
func bufferChatResponse(response *http.Response) error {
	source := response.Body
	defer source.Close()
	var reader io.Reader = source
	if response.Header.Get("Content-Encoding") == "gzip" {
		z, err := gzip.NewReader(source)
		if err != nil {
			return err
		}
		defer z.Close()
		reader = z
	}
	const limit = 32 << 20
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if len(body) > limit {
		return fmt.Errorf("上游完整响应超过兼容模式大小限制")
	}
	var data map[string]json.RawMessage
	if err = json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("解析上游完整响应: %w", err)
	}
	var choices []struct {
		Index        int                        `json:"index"`
		Message      map[string]json.RawMessage `json:"message"`
		FinishReason json.RawMessage            `json:"finish_reason"`
	}
	if err = json.Unmarshal(data["choices"], &choices); err != nil || len(choices) == 0 {
		return fmt.Errorf("上游完整响应缺少 choices")
	}
	first := make([]map[string]any, 0, len(choices))
	last := make([]map[string]any, 0, len(choices))
	for _, c := range choices {
		if c.Message == nil {
			return fmt.Errorf("上游完整响应缺少 message")
		}
		if raw, ok := c.Message["tool_calls"]; ok {
			var calls []map[string]json.RawMessage
			if err = json.Unmarshal(raw, &calls); err != nil {
				return err
			}
			for i := range calls {
				calls[i]["index"] = json.RawMessage(fmt.Sprint(i))
			}
			raw, err = json.Marshal(calls)
			if err != nil {
				return err
			}
			c.Message["tool_calls"] = raw
			if len(calls) > 0 {
				c.FinishReason = json.RawMessage(`"tool_calls"`)
			}
		}
		first = append(first, map[string]any{"index": c.Index, "delta": c.Message, "finish_reason": nil})
		last = append(last, map[string]any{"index": c.Index, "delta": map[string]any{}, "finish_reason": c.FinishReason})
	}
	data["object"] = json.RawMessage(`"chat.completion.chunk"`)
	usage := data["usage"]
	delete(data, "usage")
	var output bytes.Buffer
	emit := func(choices any) error {
		raw, e := json.Marshal(choices)
		if e != nil {
			return e
		}
		data["choices"] = raw
		raw, e = json.Marshal(data)
		if e != nil {
			return e
		}
		fmt.Fprintf(&output, "data: %s\n\n", raw)
		return nil
	}
	if err = emit(first); err != nil {
		return err
	}
	if len(usage) > 0 {
		data["usage"] = usage
	}
	if err = emit(last); err != nil {
		return err
	}
	output.WriteString("data: [DONE]\n\n")
	response.Body = io.NopCloser(bytes.NewReader(output.Bytes()))
	response.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-Length")
	response.Header.Del("ETag")
	response.ContentLength = -1
	return nil
}
