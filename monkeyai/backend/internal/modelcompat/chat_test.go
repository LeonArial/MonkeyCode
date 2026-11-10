package modelcompat

import (
	"encoding/json"
	"testing"
)

func TestAdaptChat(t *testing.T) {
	t.Setenv("MONKEYAI_CHAT_TEMPLATE_MODEL_IDS", "glm-id")
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"默认关闭", `{"model":"glm-5.3","messages":[],"tools":[{"type":"function"}]}`, false},
		{"关闭", `{"thinking":{"type":"disabled"},"chat_template_kwargs":{"other":123}}`, false},
		{"开启", `{"thinking":{"type":"enabled"}}`, true},
		{"保留显式模板开关", `{"chat_template_kwargs":{"enable_thinking":true}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := AdaptChat([]byte(tc.body), "glm-id")
			if err != nil {
				t.Fatal(err)
			}
			var d map[string]any
			json.Unmarshal(b, &d)
			if _, ok := d["thinking"]; ok {
				t.Fatal("旧参数未移除")
			}
			kwargs := d["chat_template_kwargs"].(map[string]any)
			if kwargs["enable_thinking"] != tc.want {
				t.Fatal(string(b))
			}
			if tc.name == "默认关闭" && d["tools"] == nil {
				t.Fatal("工具丢失")
			}
			if tc.name == "关闭" && kwargs["other"] != float64(123) {
				t.Fatal("其他模板参数丢失")
			}
		})
	}
	input := []byte(`{"thinking":{"type":"enabled"}}`)
	b, err := AdaptChat(input, "minimax-id")
	if err != nil || string(b) != string(input) {
		t.Fatal("误修改其他模型")
	}
	for _, input := range []string{`null`, `{"thinking":{"type":"unknown"}}`, `{"chat_template_kwargs":[]}`} {
		if _, err := AdaptChat([]byte(input), "glm-id"); err == nil {
			t.Fatal("未拒绝无效参数", input)
		}
	}
}
