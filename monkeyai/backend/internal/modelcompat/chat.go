// Package modelcompat 提供显式按模型启用的上游请求适配。
package modelcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AdaptChat 将指定模型的思考参数转换为 vLLM 模板参数，其余模型原样返回。
func AdaptChat(body []byte, modelID string) ([]byte, error) {
	enabled := false
	for _, id := range strings.Split(os.Getenv("MONKEYAI_CHAT_TEMPLATE_MODEL_IDS"), ",") {
		if modelID != "" && strings.TrimSpace(id) == modelID {
			enabled = true
			break
		}
	}
	if !enabled {
		return body, nil
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil || data == nil {
		return nil, fmt.Errorf("模型请求必须为 JSON 对象")
	}
	kwargs := map[string]json.RawMessage{}
	if raw, ok := data["chat_template_kwargs"]; ok {
		if err := json.Unmarshal(raw, &kwargs); err != nil || kwargs == nil {
			return nil, fmt.Errorf("chat_template_kwargs 必须为 JSON 对象")
		}
	}
	if _, ok := kwargs["enable_thinking"]; !ok {
		kwargs["enable_thinking"] = json.RawMessage("false")
	}
	if raw, ok := data["thinking"]; ok {
		var thinking struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &thinking); err != nil {
			return nil, fmt.Errorf("thinking 参数无效")
		}
		switch thinking.Type {
		case "enabled":
			kwargs["enable_thinking"] = json.RawMessage("true")
		case "disabled":
			kwargs["enable_thinking"] = json.RawMessage("false")
		default:
			return nil, fmt.Errorf("不支持的 thinking.type: %s", thinking.Type)
		}
		delete(data, "thinking")
	}
	raw, err := json.Marshal(kwargs)
	if err != nil {
		return nil, err
	}
	data["chat_template_kwargs"] = raw
	return json.Marshal(data)
}

// BufferedStream 仅为显式配置的模型启用非流式上游兼容。
func BufferedStream(modelID string) bool {
	if os.Getenv("MONKEYAI_CHAT_TEMPLATE_BUFFER_STREAM") != "true" {
		return false
	}
	for _, id := range strings.Split(os.Getenv("MONKEYAI_CHAT_TEMPLATE_MODEL_IDS"), ",") {
		if modelID != "" && strings.TrimSpace(id) == modelID {
			return true
		}
	}
	return false
}
