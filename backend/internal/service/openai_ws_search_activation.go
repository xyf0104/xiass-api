package service

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

const openAIWSAdditionalToolsItemType = "additional_tools"

func openAIWSRawToolsContainWebSearch(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	for _, tool := range tools.Array() {
		if strings.HasPrefix(strings.TrimSpace(tool.Get("type").String()), "web_search") {
			return true
		}
	}
	return false
}

func openAIWSMapToolsContainWebSearch(rawTools any) bool {
	tools, _ := rawTools.([]any)
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		toolType, _ := tool["type"].(string)
		if strings.HasPrefix(strings.TrimSpace(toolType), "web_search") {
			return true
		}
	}
	return false
}

func openAIWSWebSearchDeclared(payload []byte) bool {
	fields := gjson.GetMany(openAIWSPayloadStringView(payload), "tools", "input")
	if openAIWSRawToolsContainWebSearch(fields[0]) {
		return true
	}
	for _, item := range fields[1].Array() {
		if item.Get("type").String() == openAIWSAdditionalToolsItemType && openAIWSRawToolsContainWebSearch(item.Get("tools")) {
			return true
		}
	}
	return false
}

// Called under writeMu, only after the first response.create was written.
func (c *openAIWSConn) recordInitialWebSearch(value any) {
	var body []byte
	switch payload := value.(type) {
	case json.RawMessage:
		body = payload
	case []byte:
		body = payload
	case map[string]any:
		if payload["type"] != "response.create" {
			return
		}
		c.responseCreateSent = true
		c.initialWebSearch = openAIWSMapToolsContainWebSearch(payload["tools"])
		input, _ := payload["input"].([]any)
		for _, raw := range input {
			if item, ok := raw.(map[string]any); ok && item["type"] == openAIWSAdditionalToolsItemType && openAIWSMapToolsContainWebSearch(item["tools"]) {
				c.initialWebSearch = true
			}
		}
		return
	default:
		return
	}
	if gjson.GetBytes(body, "type").String() == "response.create" {
		c.responseCreateSent = true
		c.initialWebSearch = openAIWSWebSearchDeclared(body)
	}
}

func (l *openAIWSConnLease) initializedWithoutWebSearch() bool {
	c, err := l.activeConn()
	if err != nil {
		return false
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.responseCreateSent && !c.initialWebSearch
}
