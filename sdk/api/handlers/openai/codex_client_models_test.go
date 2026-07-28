package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCodexClientModelsResponseMultiAgentV2FollowsConfig(t *testing.T) {
	modelID := "codex-client-multi-agent-v2-test"
	clientID := "codex-client-multi-agent-v2-test-client"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "openai-compatibility", []*registry.ModelInfo{{ID: modelID}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	base := handlers.NewBaseAPIHandlers(&config.SDKConfig{}, nil)
	handler := NewOpenAIAPIHandler(base)
	for _, tt := range []struct {
		name    string
		enabled bool
	}{
		{name: "disabled", enabled: false},
		{name: "enabled", enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base.Cfg.CodexOptimizeMultiAgentV2 = tt.enabled
			response := handler.codexClientModelsResponse()
			models, ok := response["models"].([]map[string]any)
			if !ok {
				t.Fatalf("models type = %T, want []map[string]any", response["models"])
			}
			var entry map[string]any
			for _, model := range models {
				slug, _ := model["slug"].(string)
				if slug == modelID {
					entry = model
					break
				}
			}
			if entry == nil {
				t.Fatalf("missing synthesized model %q", modelID)
			}
			value, exists := entry["multi_agent_version"]
			if tt.enabled {
				if !exists || value != "v2" {
					t.Fatalf("multi_agent_version = %#v, want v2", value)
				}
				return
			}
			if !exists || value != nil {
				t.Fatalf("multi_agent_version = %#v, want preserved null", value)
			}
		})
	}
}

func TestOpenAIModelsAddsCapacityOnlyForCCYProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	modelID := "ccyproxy-capacity-endpoint-model"
	clientID := "ccyproxy-capacity-endpoint-client"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		ContextLength:       200000,
		MaxCompletionTokens: 64000,
	}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	handler := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&config.SDKConfig{}, nil))
	for _, tt := range []struct {
		name         string
		client       string
		wantCapacity bool
	}{
		{name: "ccyproxy", client: "ccyproxy", wantCapacity: true},
		{name: "ordinary codex client", client: "codex-cli", wantCapacity: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version="+tt.client, nil)
			handler.OpenAIModels(ctx)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Models []map[string]any `json:"models"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			for _, model := range response.Models {
				if model["slug"] != modelID {
					continue
				}
				_, hasCapacity := model["capacity_complete"]
				if hasCapacity != tt.wantCapacity {
					t.Fatalf("capacity_complete presence = %v, want %v: %#v", hasCapacity, tt.wantCapacity, model)
				}
				if tt.wantCapacity {
					if model["capacity_complete"] != true || model["max_output_tokens"] != float64(64000) || model["translation_margin_tokens"] != float64(ccyProxyDefaultTranslationMarginTokens) {
						t.Fatalf("ccyproxy capacity contract = %#v", model)
					}
				}
				return
			}
			t.Fatalf("missing model %q", modelID)
		})
	}
}

func TestCCYProxyCapacityPreservesProviderAutoCompactLimit(t *testing.T) {
	modelID := "gpt-5.6-sol"
	clientID := "ccyproxy-provider-compact-client"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "codex", []*registry.ModelInfo{{
		ID:                  modelID,
		ContextLength:       372000,
		MaxCompletionTokens: 128000,
	}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	handler := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&config.SDKConfig{}, nil))
	response := handler.codexClientModelsResponse()
	handler.addCapacityMetadata(response)
	for _, model := range response["models"].([]map[string]any) {
		if model["slug"] != modelID {
			continue
		}
		if got := model["auto_compact_token_limit"]; got != 287000 {
			t.Fatalf("auto_compact_token_limit = %#v, want 287000", got)
		}
		if got := model["translation_margin_tokens"]; got != 52000 {
			t.Fatalf("translation_margin_tokens = %#v, want 52000", got)
		}
		if got := model["capacity_complete"]; got != true {
			t.Fatalf("capacity_complete = %#v, want true", got)
		}
		return
	}
	t.Fatalf("missing model %q", modelID)
}

func TestCCYProxyCapacityCalibratesGPT56Family(t *testing.T) {
	modelIDs := []string{"gpt-5.6-terra", "gpt-5.6-luna"}
	clientID := "ccyproxy-gpt56-family-client"
	infos := make([]*registry.ModelInfo, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		infos = append(infos, &registry.ModelInfo{
			ID:                  modelID,
			ContextLength:       372000,
			MaxCompletionTokens: 128000,
		})
	}
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "codex", infos)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	handler := NewOpenAIAPIHandler(handlers.NewBaseAPIHandlers(&config.SDKConfig{}, nil))
	response := handler.codexClientModelsResponse()
	handler.addCapacityMetadata(response)
	seen := make(map[string]bool, len(modelIDs))
	for _, model := range response["models"].([]map[string]any) {
		slug, _ := model["slug"].(string)
		if slug != modelIDs[0] && slug != modelIDs[1] {
			continue
		}
		if model["auto_compact_token_limit"] != ccyProxyGPT56AutoCompactTokenLimit || model["translation_margin_tokens"] != ccyProxyGPT56TranslationMarginTokens || model["capacity_complete"] != true {
			t.Fatalf("calibrated capacity for %s = %#v", slug, model)
		}
		seen[slug] = true
	}
	for _, modelID := range modelIDs {
		if !seen[modelID] {
			t.Fatalf("missing calibrated model %q", modelID)
		}
	}
}

func TestAddCapacityMetadataReportsCompleteDeterministicContract(t *testing.T) {
	modelID := "ccyproxy-capacity-model"
	clientID := "ccyproxy-capacity-client"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		ContextLength:       200000,
		MaxCompletionTokens: 64000,
	}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	response := map[string]any{
		"models": []map[string]any{{
			"slug":                     modelID,
			"context_window":           180000,
			"auto_compact_token_limit": float64(150000),
		}},
	}
	(&OpenAIAPIHandler{}).addCapacityMetadata(response)

	model := response["models"].([]map[string]any)[0]
	if got := model["context_window"]; got != 180000 {
		t.Fatalf("context_window = %#v, want 180000", got)
	}
	if got := model["max_output_tokens"]; got != 64000 {
		t.Fatalf("max_output_tokens = %#v, want 64000", got)
	}
	if got := model["translation_margin_tokens"]; got != ccyProxyDefaultTranslationMarginTokens {
		t.Fatalf("translation_margin_tokens = %#v, want %d", got, ccyProxyDefaultTranslationMarginTokens)
	}
	if got := model["auto_compact_token_limit"]; got != 150000 {
		t.Fatalf("auto_compact_token_limit = %#v, want normalized integer", got)
	}
	if got := model["capacity_complete"]; got != true {
		t.Fatalf("capacity_complete = %#v, want true", got)
	}
	if got := model["capacity_source"]; got != "codex_client_catalog+model_registry+ccyproxy_translation_v1" {
		t.Fatalf("capacity_source = %#v", got)
	}
	blockers, ok := model["capacity_blockers"].([]string)
	if !ok || len(blockers) != 0 {
		t.Fatalf("capacity_blockers = %#v, want empty []string", model["capacity_blockers"])
	}
}

func TestAddCapacityMetadataIntersectsRegisteredCapacities(t *testing.T) {
	modelID := "ccyproxy-shared-capacity-model"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient("ccyproxy-small-capacity-client", "provider-b", []*registry.ModelInfo{{
		ID:                  modelID,
		ContextLength:       200000,
		MaxCompletionTokens: 64000,
	}})
	modelRegistry.RegisterClient("ccyproxy-large-capacity-client", "provider-a", []*registry.ModelInfo{{
		ID:                  modelID,
		ContextLength:       372000,
		MaxCompletionTokens: 128000,
	}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient("ccyproxy-large-capacity-client")
		modelRegistry.UnregisterClient("ccyproxy-small-capacity-client")
	})

	response := map[string]any{
		"models": []map[string]any{{
			"slug":           modelID,
			"context_window": 200000,
		}},
	}
	(&OpenAIAPIHandler{}).addCapacityMetadata(response)

	model := response["models"].([]map[string]any)[0]
	if model["max_output_tokens"] != 64000 || model["capacity_complete"] != true {
		t.Fatalf("conservative shared capacity = %#v", model)
	}
}

func TestAddCapacityMetadataFailsClosedOnInvalidCapacity(t *testing.T) {
	modelID := "ccyproxy-invalid-capacity-model"
	clientID := "ccyproxy-invalid-capacity-client"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		ContextLength:       100000,
		MaxCompletionTokens: 32000,
	}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	response := map[string]any{
		"models": []map[string]any{{
			"slug":                     modelID,
			"context_window":           120000,
			"auto_compact_token_limit": "unknown",
		}},
	}
	(&OpenAIAPIHandler{}).addCapacityMetadata(response)

	model := response["models"].([]map[string]any)[0]
	if got := model["capacity_complete"]; got != false {
		t.Fatalf("capacity_complete = %#v, want false", got)
	}
	if _, exists := model["translation_margin_tokens"]; exists {
		t.Fatalf("translation_margin_tokens must be absent for invalid capacity: %#v", model)
	}
	if _, exists := model["auto_compact_token_limit"]; exists {
		t.Fatalf("auto_compact_token_limit must be absent when invalid: %#v", model)
	}
	blockers, ok := model["capacity_blockers"].([]string)
	if !ok {
		t.Fatalf("capacity_blockers type = %T", model["capacity_blockers"])
	}
	want := []string{
		"active_context_window_exceeds_provider_context_window",
		"invalid_auto_compact_token_limit",
	}
	if len(blockers) != len(want) {
		t.Fatalf("capacity_blockers = %#v, want %#v", blockers, want)
	}
	for i := range want {
		if blockers[i] != want[i] {
			t.Fatalf("capacity_blockers = %#v, want %#v", blockers, want)
		}
	}
}

func TestAddCapacityMetadataFailsClosedWithoutProviderContext(t *testing.T) {
	modelID := "ccyproxy-missing-provider-context"
	clientID := "ccyproxy-missing-provider-context-client"
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID:                  modelID,
		MaxCompletionTokens: 32000,
	}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	response := map[string]any{
		"models": []map[string]any{{
			"slug":           modelID,
			"context_window": 272000,
		}},
	}
	(&OpenAIAPIHandler{}).addCapacityMetadata(response)

	model := response["models"].([]map[string]any)[0]
	if got := model["capacity_complete"]; got != false {
		t.Fatalf("capacity_complete = %#v, want false", got)
	}
	blockers, ok := model["capacity_blockers"].([]string)
	want := []string{"missing_or_invalid_max_output_tokens", "missing_or_invalid_provider_context_window"}
	if !ok || len(blockers) != len(want) {
		t.Fatalf("capacity_blockers = %#v, want %#v", model["capacity_blockers"], want)
	}
	for i := range want {
		if blockers[i] != want[i] {
			t.Fatalf("capacity_blockers = %#v, want %#v", blockers, want)
		}
	}
	if _, exists := model["translation_margin_tokens"]; exists {
		t.Fatalf("translation_margin_tokens must be absent without provider context: %#v", model)
	}
}

func TestAddCredentialAvailabilityReportsIncompleteWithoutManager(t *testing.T) {
	response := map[string]any{
		"models": []map[string]any{{"slug": "ccyproxy-no-auth-manager"}},
	}
	(&OpenAIAPIHandler{}).addCredentialAvailability(response)

	model := response["models"].([]map[string]any)[0]
	availability, ok := model["credential_availability"].(map[string]any)
	if !ok {
		t.Fatalf("credential_availability type = %T", model["credential_availability"])
	}
	if availability["status"] != "incomplete" || availability["availability_complete"] != false || availability["eligible_credentials"] != 0 {
		t.Fatalf("credential_availability = %#v", availability)
	}
	blockers, ok := availability["availability_blockers"].([]string)
	if !ok || len(blockers) != 1 || blockers[0] != "auth_manager_unavailable" {
		t.Fatalf("availability_blockers = %#v", availability["availability_blockers"])
	}
}

func TestAddCredentialAvailabilityReportsCoolingModel(t *testing.T) {
	modelID := "ccyproxy-cooling-model"
	clientID := "ccyproxy-cooling-client"
	next := time.Now().Add(time.Hour)
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient(clientID, "claude", []*registry.ModelInfo{{ID: modelID}})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	manager := coreauth.NewManager(nil, nil, nil)
	if _, errRegister := manager.Register(t.Context(), &coreauth.Auth{
		ID:       clientID,
		Provider: "claude",
		Status:   coreauth.StatusError,
		ModelStates: map[string]*coreauth.ModelState{
			modelID: {
				Status:         coreauth.StatusError,
				Unavailable:    true,
				NextRetryAfter: next,
				Quota:          coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next},
			},
		},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	base := handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager)
	handler := NewOpenAIAPIHandler(base)
	response := handler.codexClientModelsResponse()
	handler.addCredentialAvailability(response)

	models, ok := response["models"].([]map[string]any)
	if !ok {
		t.Fatalf("models type = %T, want []map[string]any", response["models"])
	}
	for _, model := range models {
		if model["slug"] != modelID {
			continue
		}
		availability, okAvailability := model["credential_availability"].(map[string]any)
		if !okAvailability {
			t.Fatalf("credential_availability type = %T", model["credential_availability"])
		}
		if availability["status"] != "cooling" || availability["eligible_credentials"] != 0 || availability["cooling_credentials"] != 1 {
			t.Fatalf("credential_availability = %#v", availability)
		}
		if _, okRetryAfter := availability["retry_after_seconds"]; !okRetryAfter {
			t.Fatalf("credential_availability missing retry_after_seconds: %#v", availability)
		}
		return
	}
	t.Fatalf("missing synthesized model %q", modelID)
}
