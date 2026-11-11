package service

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/gin-gonic/gin"
)

// Connectivity probes can accept EOF or any finish reason. A benchmark must
// see the protocol's successful terminal event, even for apparently valid HTML.
func (s *AccountTestService) processPelicanProtocolStream(c *gin.Context, body io.Reader, protocol string) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 8*benchmark.MaxHTMLBytes)
	var data strings.Builder
	finished, stopped := false, false
	process := func() error {
		if data.Len() == 0 {
			return nil
		}
		raw := strings.TrimSpace(data.String())
		data.Reset()
		if raw == "[DONE]" {
			if !finished {
				return s.pelicanError(c, "upstream_incomplete")
			}
			return nil
		}
		var event map[string]any
		if json.Unmarshal([]byte(raw), &event) != nil {
			return s.pelicanError(c, "upstream_stream_error")
		}
		if _, ok := event["error"]; ok {
			return s.pelicanStreamError(c, raw)
		}
		if protocol == "claude" {
			switch event["type"] {
			case "content_block_start":
				block, _ := event["content_block"].(map[string]any)
				if block["type"] == "text" {
					text, _ := block["text"].(string)
					s.sendEvent(c, TestEvent{Type: "content", Text: text})
				}
			case "content_block_delta":
				delta, _ := event["delta"].(map[string]any)
				if delta["type"] == "text_delta" {
					text, _ := delta["text"].(string)
					s.sendEvent(c, TestEvent{Type: "content", Text: text})
				}
			case "message_delta":
				delta, _ := event["delta"].(map[string]any)
				if reason, _ := delta["stop_reason"].(string); reason != "" {
					if reason != "end_turn" {
						return s.pelicanError(c, "upstream_incomplete")
					}
					stopped = true
				}
			case "message_stop":
				if !stopped {
					return s.pelicanError(c, "upstream_incomplete")
				}
				finished = true
			case "error":
				return s.pelicanStreamError(c, raw)
			}
			return nil
		}
		if response, ok := event["response"].(map[string]any); ok {
			event = response
		}
		if _, ok := event["error"]; ok {
			return s.pelicanStreamError(c, raw)
		}
		if feedback, ok := event["promptFeedback"].(map[string]any); ok && feedback["blockReason"] != nil {
			return s.pelicanError(c, "upstream_incomplete")
		}
		candidates, _ := event["candidates"].([]any)
		for _, item := range candidates {
			candidate, _ := item.(map[string]any)
			if index, ok := candidate["index"].(float64); ok && index != 0 {
				continue
			}
			content, _ := candidate["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, item := range parts {
				part, _ := item.(map[string]any)
				if thought, _ := part["thought"].(bool); thought {
					continue
				}
				if text, ok := part["text"].(string); ok {
					s.sendEvent(c, TestEvent{Type: "content", Text: text})
				}
			}
			if reason, _ := candidate["finishReason"].(string); reason != "" {
				if reason != "STOP" {
					return s.pelicanError(c, "upstream_incomplete")
				}
				finished = true
			}
			break
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := process(); err != nil {
				return err
			}
			if finished {
				s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len()+len(line) > 8*benchmark.MaxHTMLBytes {
				return s.pelicanError(c, "upstream_stream_error")
			}
			data.WriteString(strings.TrimPrefix(line, "data:"))
			data.WriteByte('\n')
		}
	}
	if err := process(); err != nil {
		return err
	}
	if scanner.Err() != nil {
		return s.pelicanError(c, "upstream_stream_error")
	}
	if !finished {
		return s.pelicanError(c, "upstream_incomplete")
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *AccountTestService) processPelicanClaudeJSON(c *gin.Context, body io.Reader) error {
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string          `json:"stop_reason"`
		Error      json.RawMessage `json:"error"`
	}
	decoder := json.NewDecoder(body)
	if decoder.Decode(&response) != nil {
		return s.pelicanError(c, "upstream_stream_error")
	}
	if len(response.Error) > 0 {
		return s.pelicanStreamError(c, `{"error":`+string(response.Error)+`}`)
	}
	for _, block := range response.Content {
		if block.Type == "text" {
			s.sendEvent(c, TestEvent{Type: "content", Text: block.Text})
		}
	}
	var extra any
	if response.StopReason != "end_turn" || decoder.Decode(&extra) != io.EOF {
		return s.pelicanError(c, "upstream_incomplete")
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
