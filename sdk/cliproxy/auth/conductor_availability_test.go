package auth

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

func TestUpdateAggregatedAvailability_UnavailableWithoutNextRetryDoesNotBlockAuth(t *testing.T) {
	t.Parallel()

	now := time.Now()
	model := "test-model"
	auth := &Auth{
		ID: "a",
		ModelStates: map[string]*ModelState{
			model: {
				Status:      StatusError,
				Unavailable: true,
			},
		},
	}

	updateAggregatedAvailability(auth, now)

	if auth.Unavailable {
		t.Fatalf("auth.Unavailable = true, want false")
	}
	if !auth.NextRetryAfter.IsZero() {
		t.Fatalf("auth.NextRetryAfter = %v, want zero", auth.NextRetryAfter)
	}
}

func TestUpdateAggregatedAvailability_FutureNextRetryBlocksAuth(t *testing.T) {
	t.Parallel()

	now := time.Now()
	model := "test-model"
	next := now.Add(5 * time.Minute)
	auth := &Auth{
		ID: "a",
		ModelStates: map[string]*ModelState{
			model: {
				Status:         StatusError,
				Unavailable:    true,
				NextRetryAfter: next,
			},
		},
	}

	updateAggregatedAvailability(auth, now)

	if !auth.Unavailable {
		t.Fatalf("auth.Unavailable = false, want true")
	}
	if auth.NextRetryAfter.IsZero() {
		t.Fatalf("auth.NextRetryAfter = zero, want %v", next)
	}
	if auth.NextRetryAfter.Sub(next) > time.Second || next.Sub(auth.NextRetryAfter) > time.Second {
		t.Fatalf("auth.NextRetryAfter = %v, want %v", auth.NextRetryAfter, next)
	}
}

func TestManager_AvailableProvidersAndHasProviderAuth_ExcludeDisabled(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	ctx := context.Background()

	if _, err := manager.Register(ctx, &Auth{ID: "active", Provider: "claude", Status: StatusActive}); err != nil {
		t.Fatalf("register active auth: %v", err)
	}
	// Provider gemini only has an auth with the Disabled flag set.
	if _, err := manager.Register(ctx, &Auth{ID: "flag-disabled", Provider: "gemini", Disabled: true}); err != nil {
		t.Fatalf("register flag-disabled auth: %v", err)
	}
	// Provider codex only has an auth whose Status is StatusDisabled.
	if _, err := manager.Register(ctx, &Auth{ID: "status-disabled", Provider: "codex", Status: StatusDisabled}); err != nil {
		t.Fatalf("register status-disabled auth: %v", err)
	}

	providers := manager.AvailableProviders()
	present := make(map[string]bool, len(providers))
	for _, p := range providers {
		present[p] = true
	}
	if !present["claude"] {
		t.Errorf("AvailableProviders() = %v, want to include active provider claude", providers)
	}
	if present["gemini"] {
		t.Errorf("AvailableProviders() = %v, want to exclude Disabled provider gemini", providers)
	}
	if present["codex"] {
		t.Errorf("AvailableProviders() = %v, want to exclude StatusDisabled provider codex", providers)
	}

	if !manager.HasProviderAuth("claude") {
		t.Errorf("HasProviderAuth(claude) = false, want true")
	}
	if manager.HasProviderAuth("gemini") {
		t.Errorf("HasProviderAuth(gemini) = true, want false (only Disabled auth registered)")
	}
	if manager.HasProviderAuth("codex") {
		t.Errorf("HasProviderAuth(codex) = true, want false (only StatusDisabled auth registered)")
	}
}

func TestManager_ResetQuotaClearsRuntimeAndRegistryState(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	ctx := context.Background()
	authID := "reset-quota-auth"
	model := "reset-quota-model"
	next := time.Now().Add(time.Hour)

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(authID)
	})

	if _, errRegister := manager.Register(ctx, &Auth{
		ID:             authID,
		Provider:       "claude",
		Status:         StatusError,
		StatusMessage:  "quota exhausted",
		Unavailable:    true,
		NextRetryAfter: next,
		Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next, BackoffLevel: 2},
		ModelStates: map[string]*ModelState{
			model: {
				Status:         StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: next,
				Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next, BackoffLevel: 2},
				UpdatedAt:      next,
			},
		},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	reg.SetModelQuotaExceeded(authID, model)
	reg.SuspendClientModel(authID, model, "quota")
	if count := reg.GetModelCount(model); count != 0 {
		t.Fatalf("registry model count before reset = %d, want 0", count)
	}

	updated, models, errReset := manager.ResetQuota(ctx, authID)
	if errReset != nil {
		t.Fatalf("ResetQuota() error = %v", errReset)
	}
	if updated == nil {
		t.Fatalf("ResetQuota() updated auth is nil")
	}
	if len(models) != 1 || models[0] != model {
		t.Fatalf("ResetQuota() models = %v, want [%s]", models, model)
	}
	if updated.Status != StatusActive || updated.StatusMessage != "" || updated.Unavailable || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("updated auth state = status %q message %q unavailable %v next %v", updated.Status, updated.StatusMessage, updated.Unavailable, updated.NextRetryAfter)
	}
	if updated.Quota.Exceeded || updated.Quota.Reason != "" || !updated.Quota.NextRecoverAt.IsZero() || updated.Quota.BackoffLevel != 0 {
		t.Fatalf("updated auth quota = %+v, want cleared", updated.Quota)
	}
	state := updated.ModelStates[model]
	if state == nil {
		t.Fatalf("updated model state missing")
	}
	if state.Status != StatusActive || state.StatusMessage != "" || state.Unavailable || !state.NextRetryAfter.IsZero() {
		t.Fatalf("updated model state = status %q message %q unavailable %v next %v", state.Status, state.StatusMessage, state.Unavailable, state.NextRetryAfter)
	}
	if state.Quota.Exceeded || state.Quota.Reason != "" || !state.Quota.NextRecoverAt.IsZero() || state.Quota.BackoffLevel != 0 {
		t.Fatalf("updated model quota = %+v, want cleared", state.Quota)
	}
	if count := reg.GetModelCount(model); count != 1 {
		t.Fatalf("registry model count after reset = %d, want 1", count)
	}
}

func TestManager_ReconcileRegistryModelStatesPreservesActiveCooldown(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	ctx := context.Background()
	authID := "reconcile-active-cooldown-auth"
	model := "reconcile-active-cooldown-model"
	next := time.Now().Add(2 * time.Hour).Round(time.Second)

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(authID)
	})

	if _, errRegister := manager.Register(ctx, &Auth{
		ID:       authID,
		Provider: "claude",
		Status:   StatusError,
		ModelStates: map[string]*ModelState{
			model: {
				Status:         StatusError,
				StatusMessage:  "quota exhausted",
				Unavailable:    true,
				NextRetryAfter: next,
				Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next},
			},
		},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	reg.SetModelQuotaExceeded(authID, model)
	reg.SuspendClientModel(authID, model, "quota")
	if count := reg.GetModelCount(model); count != 0 {
		t.Fatalf("registry model count before refresh = %d, want 0", count)
	}

	// OAuth refresh re-registers the same client/model and clears the registry
	// snapshot before Manager reconciliation runs.
	reg.RegisterClient(authID, "claude", []*registry.ModelInfo{{ID: model}})
	if count := reg.GetModelCount(model); count != 1 {
		t.Fatalf("registry model count immediately after refresh = %d, want 1", count)
	}

	manager.ReconcileRegistryModelStates(ctx, authID)

	updated, ok := manager.GetByID(authID)
	if !ok || updated == nil {
		t.Fatal("expected reconciled auth")
	}
	state := updated.ModelStates[model]
	if state == nil || !state.Unavailable || !state.Quota.Exceeded {
		t.Fatalf("active cooldown was not preserved: %+v", state)
	}
	if !state.NextRetryAfter.Equal(next) {
		t.Fatalf("NextRetryAfter = %v, want %v", state.NextRetryAfter, next)
	}
	if count := reg.GetModelCount(model); count != 0 {
		t.Fatalf("registry model count after reconciliation = %d, want 0", count)
	}
}

func TestManager_ReconcileRegistryModelStatesClearsExpiredCooldown(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	ctx := context.Background()
	authID := "reconcile-expired-cooldown-auth"
	model := "reconcile-expired-cooldown-model"
	expired := time.Now().Add(-time.Minute)

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "claude", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(authID)
	})

	if _, errRegister := manager.Register(ctx, &Auth{
		ID:       authID,
		Provider: "claude",
		ModelStates: map[string]*ModelState{
			model: {
				Status:         StatusError,
				Unavailable:    true,
				NextRetryAfter: expired,
				Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: expired},
			},
		},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	manager.ReconcileRegistryModelStates(ctx, authID)

	updated, ok := manager.GetByID(authID)
	if !ok || updated == nil {
		t.Fatal("expected reconciled auth")
	}
	state := updated.ModelStates[model]
	if state == nil || !modelStateIsClean(state) {
		t.Fatalf("expired cooldown was not reset: %+v", state)
	}
	if count := reg.GetModelCount(model); count != 1 {
		t.Fatalf("registry model count after expired reconciliation = %d, want 1", count)
	}
}

func TestManager_AvailabilityForModelReportsEligibilityAndCooldown(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	ctx := context.Background()
	model := "availability-summary-model"
	next := time.Now().Add(time.Hour)
	auths := []*Auth{
		{ID: "availability-eligible-auth", Provider: "claude", Status: StatusActive},
		{
			ID:       "availability-cooling-auth",
			Provider: "claude",
			Status:   StatusError,
			ModelStates: map[string]*ModelState{
				model: {
					Status:         StatusError,
					Unavailable:    true,
					NextRetryAfter: next,
					Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: next},
				},
			},
		},
		{ID: "availability-disabled-auth", Provider: "claude", Status: StatusDisabled, Disabled: true},
	}

	reg := registry.GetGlobalRegistry()
	for _, candidate := range auths {
		reg.RegisterClient(candidate.ID, candidate.Provider, []*registry.ModelInfo{{ID: model}})
		if _, errRegister := manager.Register(ctx, candidate); errRegister != nil {
			t.Fatalf("register %s: %v", candidate.ID, errRegister)
		}
	}
	t.Cleanup(func() {
		for _, candidate := range auths {
			reg.UnregisterClient(candidate.ID)
		}
	})

	summary := manager.AvailabilityForModel(model)
	if summary.TotalCredentials != 3 || summary.EligibleCredentials != 1 || summary.CoolingCredentials != 1 || summary.BlockedCredentials != 1 {
		t.Fatalf("AvailabilityForModel() = %+v", summary)
	}
	if summary.CooldownUntil.IsZero() || summary.CooldownUntil.Sub(next) > time.Second || next.Sub(summary.CooldownUntil) > time.Second {
		t.Fatalf("CooldownUntil = %v, want %v", summary.CooldownUntil, next)
	}
}
