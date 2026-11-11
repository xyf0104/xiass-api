package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

// AvailableAntigravityModels uses this account's live catalog, never the global
// picker defaults. Capability sync is identity-bound and does not change routing
// preferences, model cooldowns or scheduling state.
func (s *AccountTestService) AvailableAntigravityModels(ctx context.Context, account *Account) ([]antigravity.ClaudeModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	catalog, err := s.SyncUpstreamModelCatalog(ctx, account)
	if err != nil {
		return nil, err
	}
	return antigravityAvailableModelOptions(account, catalog), nil
}

func antigravityAvailableModelOptions(account *Account, catalog *UpstreamModelCatalog) []antigravity.ClaudeModel {
	result := make([]antigravity.ClaudeModel, 0)
	if account == nil || catalog == nil {
		return result
	}
	ids := publicAntigravityModelIDs(catalog.Models)
	available := make(map[string]bool, len(ids))
	for _, id := range ids {
		available[normalizeAntigravityCompatModel(id)] = true
	}
	// A configured public alias is callable when its final target exists in this
	// account's catalog. Keep aliases without exposing wildcard/internal routes.
	candidates := append([]string(nil), ids...)
	for source := range account.GetModelMapping() {
		if !strings.Contains(source, "*") {
			candidates = append(candidates, source)
		}
	}
	labels := make(map[string]antigravity.ClaudeModel)
	for _, model := range antigravity.DefaultModels() {
		labels[model.ID] = model
	}
	for _, id := range publicAntigravityModelIDs(candidates) {
		mapped := mapAntigravityModel(account, id)
		if mapped == "" {
			continue
		}
		// Public Flash tiers share one internal generation route. A tier is
		// advertised only when that exact public tier came from the live catalog.
		finalID := normalizeAntigravityCompatModel(mapped)
		if family, ok := antigravityGeminiFlashFamilyForModel(mapped); ok && isAntigravityGeminiFlashInternalModel(mapped) {
			if source, sourceOK := antigravityGeminiFlashFamilyForModel(id); sourceOK && source.tieredModel == family.tieredModel {
				finalID = normalizeAntigravityCompatModel(id)
			}
		}
		if !available[finalID] {
			continue
		}
		model, known := labels[id]
		if !known {
			model = antigravity.ClaudeModel{ID: id, Type: "model", DisplayName: id}
			if label := strings.TrimSpace(catalog.Metadata[id].DisplayName); label != "" {
				model.DisplayName = label
			}
		}
		result = append(result, model)
	}
	return result
}
