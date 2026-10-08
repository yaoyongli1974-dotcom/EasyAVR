package policy

import (
	"fmt"
	"sync"

	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
)

// PolicyEngine wraps Casbin enforcer with domain-specific helpers.
type PolicyEngine struct {
	enforcer *casbin.SyncedEnforcer
	mu       sync.RWMutex
}

// NewPolicyEngine creates a new policy engine with GORM adapter.
func NewPolicyEngine(db *gorm.DB) (*PolicyEngine, error) {
	// Initialize GORM adapter
	adapter, err := gormadapter.NewAdapterByDB(db)
	if err != nil {
		return nil, fmt.Errorf("casbin gorm adapter: %w", err)
	}

	// Load model from text (RBAC + ABAC for AI resources)
	m, err := model.NewModelFromString(policyModel)
	if err != nil {
		return nil, fmt.Errorf("casbin model: %w", err)
	}

	enforcer, err := casbin.NewSyncedEnforcer(m, adapter)
	if err != nil {
		return nil, fmt.Errorf("casbin enforcer: %w", err)
	}

	// Enable auto-save so policy changes persist immediately
	enforcer.EnableAutoSave(true)

	pe := &PolicyEngine{enforcer: enforcer}

	// Load existing policies from DB
	if err := enforcer.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}

	return pe, nil
}

// Enforcer returns the underlying Casbin enforcer for advanced usage.
func (pe *PolicyEngine) Enforcer() *casbin.SyncedEnforcer {
	return pe.enforcer
}

// CheckPermission checks if a subject (user/role) can perform action on resource.
// subject: "user:123" or "role:admin"
// resource: "ai:task", "ai:model", "device", "channel", "user", "role", "group", etc.
// action: "create", "read", "update", "delete", "start", "stop", "deploy", "infer", etc.
// domain: optional tenant/domain (e.g., group ID for group-scoped resources)
func (pe *PolicyEngine) CheckPermission(subject, resource, action, domain string) (bool, error) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.enforcer.Enforce(subject, resource, action, domain)
}

// CheckPermissionForUser checks permission for a user, automatically including their roles.
func (pe *PolicyEngine) CheckPermissionForUser(userID uint, username, role string, resource, action, domain string) (bool, error) {
	// Check user-level policy first
	sub := fmt.Sprintf("user:%d", userID)
	if ok, err := pe.CheckPermission(sub, resource, action, domain); err != nil || ok {
		return ok, err
	}
	// Check role-level policy
	if role != "" {
		sub = fmt.Sprintf("role:%s", role)
		if ok, err := pe.CheckPermission(sub, resource, action, domain); err != nil || ok {
			return ok, err
		}
	}
	// Check username-based policy (for backward compat)
	sub = fmt.Sprintf("user:%s", username)
	return pe.CheckPermission(sub, resource, action, domain)
}

// AddPolicy adds a new policy rule (p_type).
func (pe *PolicyEngine) AddPolicy(params ...string) (bool, error) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	args := make([]interface{}, len(params))
	for i, p := range params {
		args[i] = p
	}
	return pe.enforcer.AddPolicy(args...)
}

// RemovePolicy removes a policy rule.
func (pe *PolicyEngine) RemovePolicy(params ...string) (bool, error) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	args := make([]interface{}, len(params))
	for i, p := range params {
		args[i] = p
	}
	return pe.enforcer.RemovePolicy(args...)
}

// AddGroupingPolicy adds a new grouping policy rule (g_type).
func (pe *PolicyEngine) AddGroupingPolicy(params ...string) (bool, error) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	args := make([]interface{}, len(params))
	for i, p := range params {
		args[i] = p
	}
	return pe.enforcer.AddGroupingPolicy(args...)
}

// RemoveGroupingPolicy removes a grouping policy rule.
func (pe *PolicyEngine) RemoveGroupingPolicy(params ...string) (bool, error) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	args := make([]interface{}, len(params))
	for i, p := range params {
		args[i] = p
	}
	return pe.enforcer.RemoveGroupingPolicy(args...)
}

// GetAllPolicies returns all policy rules.
func (pe *PolicyEngine) GetAllPolicies() ([][]string, error) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.enforcer.GetPolicy()
}

// GetGroupingPolicies returns all role assignments.
func (pe *PolicyEngine) GetGroupingPolicies() ([][]string, error) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.enforcer.GetGroupingPolicy()
}

// GetImplicitRolesForUser returns all roles a user has (including inherited).
func (pe *PolicyEngine) GetImplicitRolesForUser(user string) ([]string, error) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.enforcer.GetImplicitRolesForUser(user)
}

// SeedDefaultPolicies seeds the default policies for backward compatibility with existing RBAC.
// It is idempotent: if any policies already exist, it does nothing.
func (pe *PolicyEngine) SeedDefaultPolicies() error {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	existing, err := pe.enforcer.GetPolicy()
	if err != nil {
		return fmt.Errorf("read existing policies: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}
	return pe.applyDefaultPolicies()
}

// ResetDefaultPolicies clears all policies and re-seeds the built-in defaults.
func (pe *PolicyEngine) ResetDefaultPolicies() error {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.applyDefaultPolicies()
}

// applyDefaultPolicies clears the policy set and writes the built-in defaults.
// Callers must hold pe.mu.
func (pe *PolicyEngine) applyDefaultPolicies() error {
	pe.enforcer.ClearPolicy()

	// Built-in roles and their permissions (subject, resource, action, domain)
	// Admin: full access to everything
	adminPerms := [][]string{
		{"role:admin", "*", "*", "*"},
	}

	// Operator: mirrors the built-in "operator" RBAC permissions, plus the
	// resources that were previously accessible to any authenticated user
	// (video/ai/events/gb28181/ga1400).
	operatorPerms := [][]string{
		{"role:operator", "video", "*", "*"},
		{"role:operator", "ai:provider", "*", "*"},
		{"role:operator", "ai:task", "*", "*"},
		{"role:operator", "ai:search", "*", "*"},
		{"role:operator", "ai:model", "*", "*"},
		{"role:operator", "ai:deployment", "*", "*"},
		{"role:operator", "ai:dataset", "*", "*"},
		{"role:operator", "ai:annotation", "*", "*"},
		{"role:operator", "ai:training", "*", "*"},
		{"role:operator", "event", "*", "*"},
		{"role:operator", "alert", "*", "*"},
		{"role:operator", "gb28181", "*", "*"},
		{"role:operator", "ga1400", "*", "*"},
	}

	// Viewer: read-only video/event/search.
	viewerPerms := [][]string{
		{"role:viewer", "video", "*", "*"},
		{"role:viewer", "event", "*", "*"},
		{"role:viewer", "ai:search", "*", "*"},
	}

	allPerms := append(adminPerms, operatorPerms...)
	allPerms = append(allPerms, viewerPerms...)

	for _, p := range allPerms {
		args := make([]interface{}, len(p))
		for i, v := range p {
			args[i] = v
		}
		if _, err := pe.enforcer.AddPolicy(args...); err != nil {
			return fmt.Errorf("seed policy %v: %w", p, err)
		}
	}

	return pe.enforcer.SavePolicy()
}

// policyModel defines the Casbin model with RBAC + ABAC.
// Request: subject, resource, action, domain
// Policy: p_type, subject, resource, action, domain
// Grouping: g_type, user, role, domain
const policyModel = `
[request_definition]
r = sub, obj, act, dom

[policy_definition]
p = sub, obj, act, dom

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub, r.dom) && keyMatch(r.obj, p.obj) && (r.act == p.act || p.act == "*") && (r.dom == p.dom || p.dom == "*")
`
