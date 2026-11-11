package service

import "github.com/tidwall/gjson"

// Non-stream clients need the response envelope even when generation was stopped.
func extractOpenAINonCompletedTerminalResponse(terminalType string, payload []byte) ([]byte, bool) {
	switch terminalType {
	case "response.incomplete", "response.cancelled", "response.canceled":
	default:
		return nil, false
	}
	if response := gjson.GetBytes(payload, "response"); response.IsObject() {
		return []byte(response.Raw), true
	}
	return nil, false
}
