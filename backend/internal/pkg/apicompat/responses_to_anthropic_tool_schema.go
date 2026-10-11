package apicompat

import (
	"bytes"
	"encoding/json"
)

var anthropicInputSchemaUnionKeywords = []string{"oneOf", "anyOf", "allOf"}

// flattenAnthropicRootUnions converts root unions into one object schema.
// Alternative-branch properties are merged with anyOf, allOf branch properties
// remain conjunctive, and independent root keywords are combined conjunctively.
// Nested schemas are preserved verbatim.
func flattenAnthropicRootUnions(schema map[string]json.RawMessage) {
	present := make([]string, 0, len(anthropicInputSchemaUnionKeywords))
	for _, keyword := range anthropicInputSchemaUnionKeywords {
		if _, ok := schema[keyword]; ok {
			present = append(present, keyword)
		}
	}
	if len(present) == 0 {
		return
	}

	properties := anthropicSchemaProperties(schema)
	required := anthropicBranchRequired(schema)

	for _, keyword := range present {
		raw := schema[keyword]
		delete(schema, keyword)

		branches, ok := decodeAnthropicUnionBranches(raw)
		if !ok {
			continue
		}
		keywordProperties := anthropicUnionBranchProperties(branches, keyword)
		mergeAnthropicConjunctiveProperties(properties, keywordProperties)
		if keyword == "allOf" {
			for _, branch := range branches {
				required = appendAnthropicRequired(required, anthropicBranchRequired(branch))
			}
			continue
		}
		required = appendAnthropicRequired(required, intersectAnthropicRequired(branches))
	}

	if len(properties) > 0 {
		if encoded, err := json.Marshal(properties); err == nil {
			schema["properties"] = encoded
		}
	}
	if len(required) > 0 {
		if encoded, err := json.Marshal(required); err == nil {
			schema["required"] = encoded
		}
	} else {
		delete(schema, "required")
	}
}

func anthropicSchemaProperties(schema map[string]json.RawMessage) map[string]json.RawMessage {
	properties := make(map[string]json.RawMessage)
	raw, ok := schema["properties"]
	if !ok {
		return properties
	}
	if err := json.Unmarshal(raw, &properties); err != nil || properties == nil {
		return make(map[string]json.RawMessage)
	}
	return properties
}

func decodeAnthropicUnionBranches(raw json.RawMessage) ([]map[string]json.RawMessage, bool) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, false
	}
	branches := make([]map[string]json.RawMessage, 0, len(items))
	for _, item := range items {
		var branch map[string]json.RawMessage
		if err := json.Unmarshal(item, &branch); err != nil || branch == nil {
			branches = append(branches, nil)
			continue
		}
		flattenAnthropicRootUnions(branch)
		branches = append(branches, branch)
	}
	return branches, true
}

func anthropicUnionBranchProperties(branches []map[string]json.RawMessage, keyword string) map[string]json.RawMessage {
	properties := make(map[string]json.RawMessage)
	mergeKeyword := "anyOf"
	if keyword == "allOf" {
		mergeKeyword = "allOf"
	}
	for _, branch := range branches {
		if !anthropicSchemaBranchIsObject(branch) {
			continue
		}
		var branchProperties map[string]json.RawMessage
		if err := json.Unmarshal(branch["properties"], &branchProperties); err != nil {
			continue
		}
		for name, property := range branchProperties {
			existing, seen := properties[name]
			if !seen {
				properties[name] = property
				continue
			}
			if anthropicSchemaJSONEqual(existing, property) {
				continue
			}
			merged, err := mergeAnthropicPropertySchemas(existing, property, mergeKeyword)
			if err == nil {
				properties[name] = merged
			}
		}
	}
	return properties
}

func mergeAnthropicConjunctiveProperties(into, contribution map[string]json.RawMessage) {
	for name, property := range contribution {
		existing, seen := into[name]
		if !seen {
			into[name] = property
			continue
		}
		if anthropicSchemaJSONEqual(existing, property) {
			continue
		}
		merged, err := mergeAnthropicPropertySchemas(existing, property, "allOf")
		if err == nil {
			into[name] = merged
		}
	}
}

func mergeAnthropicPropertySchemas(left, right json.RawMessage, keyword string) (json.RawMessage, error) {
	return json.Marshal(map[string][]json.RawMessage{keyword: {left, right}})
}

func anthropicSchemaBranchIsObject(branch map[string]json.RawMessage) bool {
	if branch == nil {
		return false
	}
	if raw, ok := branch["type"]; ok {
		var typ string
		return json.Unmarshal(raw, &typ) == nil && typ == "object"
	}
	_, ok := branch["properties"]
	return ok
}

func anthropicBranchRequired(branch map[string]json.RawMessage) []string {
	if branch == nil {
		return nil
	}
	var required []string
	if err := json.Unmarshal(branch["required"], &required); err != nil {
		return nil
	}
	return required
}

func intersectAnthropicRequired(branches []map[string]json.RawMessage) []string {
	lists := make([][]string, 0, len(branches))
	for _, branch := range branches {
		lists = append(lists, anthropicBranchRequired(branch))
	}
	return intersectAnthropicRequiredLists(lists)
}

func intersectAnthropicRequiredLists(lists [][]string) []string {
	if len(lists) == 0 {
		return nil
	}
	result := append([]string(nil), lists[0]...)
	for _, list := range lists[1:] {
		next := make([]string, 0, len(result))
		for _, name := range result {
			if containsAnthropicName(list, name) {
				next = append(next, name)
			}
		}
		result = next
	}
	return result
}

func appendAnthropicRequired(required, names []string) []string {
	merged := append([]string(nil), required...)
	for _, name := range names {
		if !containsAnthropicName(merged, name) {
			merged = append(merged, name)
		}
	}
	return merged
}

func containsAnthropicName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func anthropicSchemaJSONEqual(left, right json.RawMessage) bool {
	var leftBuf, rightBuf bytes.Buffer
	if json.Compact(&leftBuf, left) != nil || json.Compact(&rightBuf, right) != nil {
		return false
	}
	return leftBuf.String() == rightBuf.String()
}
