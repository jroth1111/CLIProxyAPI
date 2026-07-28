package openai

import (
	"math"
	"time"

	codexmodels "github.com/router-for-me/CLIProxyAPI/v7/internal/client/codex/models"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

const (
	ccyProxyDefaultTranslationMarginTokens = 13000
	ccyProxyGPT56TranslationMarginTokens   = 52000
	ccyProxyGPT56AutoCompactTokenLimit     = 287000
)

func isCCYProxyGPT56Model(slug string) bool {
	switch slug {
	case "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
		return true
	default:
		return false
	}
}

func (h *OpenAIAPIHandler) codexClientModelsResponse() map[string]any {
	optimizeMultiAgentV2 := h != nil && h.Cfg != nil && h.Cfg.CodexOptimizeMultiAgentV2
	return codexmodels.BuildResponse(h.Models(), registry.GetGlobalRegistry().GetModelProviders, optimizeMultiAgentV2)
}

func (h *OpenAIAPIHandler) addCapacityMetadata(response map[string]any) {
	if response == nil {
		return
	}
	models, ok := response["models"].([]map[string]any)
	if !ok {
		return
	}
	for _, model := range models {
		slug, _ := model["slug"].(string)
		blockers := make([]string, 0, 3)
		rawTranslationMargin, hasTranslationMargin := model["translation_margin_tokens"]
		delete(model, "max_output_tokens")
		delete(model, "translation_margin_tokens")
		contextWindow, validContextWindow := positiveCapacityInteger(model["context_window"])
		if !validContextWindow {
			blockers = append(blockers, "missing_or_invalid_context_window")
		}

		info := registry.LookupModelInfo(slug)
		providerContextWindow := 0
		maxOutputTokens := 0
		if info != nil {
			providerContextWindow = info.ContextLength
			if providerContextWindow == 0 {
				providerContextWindow = info.InputTokenLimit
			}
			maxOutputTokens = info.MaxCompletionTokens
			if maxOutputTokens == 0 {
				maxOutputTokens = info.OutputTokenLimit
			}
		}
		if maxOutputTokens <= 0 {
			blockers = append(blockers, "missing_or_invalid_max_output_tokens")
		} else {
			model["max_output_tokens"] = maxOutputTokens
		}

		if validContextWindow {
			translationMargin := ccyProxyDefaultTranslationMarginTokens
			validTranslationMargin := true
			if isCCYProxyGPT56Model(slug) {
				translationMargin = ccyProxyGPT56TranslationMarginTokens
			} else if hasTranslationMargin {
				translationMargin, validTranslationMargin = positiveCapacityInteger(rawTranslationMargin)
			}
			if !validTranslationMargin {
				blockers = append(blockers, "invalid_translation_margin_tokens")
			} else {
				model["translation_margin_tokens"] = translationMargin
			}
			if providerContextWindow <= 0 {
				delete(model, "translation_margin_tokens")
				blockers = append(blockers, "missing_or_invalid_provider_context_window")
			} else if contextWindow > providerContextWindow {
				delete(model, "translation_margin_tokens")
				blockers = append(blockers, "active_context_window_exceeds_provider_context_window")
			}
		}

		if isCCYProxyGPT56Model(slug) {
			if !validContextWindow || ccyProxyGPT56AutoCompactTokenLimit >= contextWindow {
				delete(model, "auto_compact_token_limit")
				blockers = append(blockers, "invalid_auto_compact_token_limit")
			} else {
				model["auto_compact_token_limit"] = ccyProxyGPT56AutoCompactTokenLimit
			}
		} else if rawCompactLimit, exists := model["auto_compact_token_limit"]; exists && rawCompactLimit != nil {
			compactLimit, validCompactLimit := positiveCapacityInteger(rawCompactLimit)
			if !validCompactLimit || validContextWindow && compactLimit >= contextWindow {
				delete(model, "auto_compact_token_limit")
				blockers = append(blockers, "invalid_auto_compact_token_limit")
			} else {
				model["auto_compact_token_limit"] = compactLimit
			}
		} else {
			delete(model, "auto_compact_token_limit")
		}

		model["capacity_complete"] = len(blockers) == 0
		model["capacity_source"] = "codex_client_catalog+model_registry+ccyproxy_translation_v1"
		model["capacity_blockers"] = blockers
	}
}

func positiveCapacityInteger(value any) (int, bool) {
	var number int64
	switch typed := value.(type) {
	case int:
		number = int64(typed)
	case int32:
		number = int64(typed)
	case int64:
		number = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, false
		}
		number = int64(typed)
	default:
		return 0, false
	}
	if number <= 0 || int64(int(number)) != number {
		return 0, false
	}
	return int(number), true
}

func (h *OpenAIAPIHandler) addCredentialAvailability(response map[string]any) {
	if h == nil || h.AuthManager == nil || response == nil {
		return
	}
	models, ok := response["models"].([]map[string]any)
	if !ok {
		return
	}
	now := time.Now()
	for _, model := range models {
		slug, _ := model["slug"].(string)
		if slug == "" {
			continue
		}
		summary := h.AuthManager.AvailabilityForModel(slug)
		status := "unavailable"
		if summary.EligibleCredentials > 0 {
			status = "available"
		} else if summary.TotalCredentials > 0 && summary.CoolingCredentials > 0 && summary.BlockedCredentials == 0 {
			status = "cooling"
		}
		metadata := map[string]any{
			"status":               status,
			"total_credentials":    summary.TotalCredentials,
			"eligible_credentials": summary.EligibleCredentials,
			"cooling_credentials":  summary.CoolingCredentials,
			"blocked_credentials":  summary.BlockedCredentials,
		}
		if summary.CooldownUntil.After(now) {
			retryAfter := int(math.Ceil(summary.CooldownUntil.Sub(now).Seconds()))
			metadata["retry_after_seconds"] = retryAfter
			metadata["cooldown_until"] = summary.CooldownUntil.Format(time.RFC3339)
		}
		model["credential_availability"] = metadata
	}
}

// CodexClientModelsResponse builds a Codex client model response.
func CodexClientModelsResponse(models []map[string]any) map[string]any {
	return codexmodels.BuildResponse(models, nil, false)
}

// CodexClientModelsResponseWithMultiAgentV2 builds a Codex client model response
// and advertises multi-agent v2 for synthesized models when enabled.
func CodexClientModelsResponseWithMultiAgentV2(models []map[string]any, enabled bool) map[string]any {
	return codexmodels.BuildResponse(models, nil, enabled)
}
