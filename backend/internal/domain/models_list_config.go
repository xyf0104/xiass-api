package domain

// GroupModelsListConfig is retained as a source-compatibility alias while
// existing XIASS integrations migrate to the upstream model_allowlist field.
// New code should use GroupModelAllowlist.
type GroupModelsListConfig = GroupModelAllowlist
