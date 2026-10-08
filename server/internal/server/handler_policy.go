package server

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- policy management ----

type policyRuleReq struct {
	PType  string   `json:"pType"` // "p" or "g"
	Params []string `json:"params"`
}

func (a *App) listPolicies(c *gin.Context) {
	policies, err := a.policy.GetAllPolicies()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	grouping, err := a.policy.GetGroupingPolicies()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	// Format for frontend
	type policyItem struct {
		PType  string   `json:"pType"`
		Params []string `json:"params"`
	}
	var items []policyItem
	for _, p := range policies {
		items = append(items, policyItem{PType: "p", Params: p})
	}
	for _, g := range grouping {
		items = append(items, policyItem{PType: "g", Params: g})
	}
	ok(c, items)
}

func (a *App) addPolicy(c *gin.Context) {
	var req policyRuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.PType != "p" && req.PType != "g" {
		fail(c, http.StatusBadRequest, "pType must be 'p' or 'g'")
		return
	}
	if len(req.Params) < 3 {
		fail(c, http.StatusBadRequest, "params must have at least 3 elements")
		return
	}

	var added bool
	var err error
	if req.PType == "p" {
		added, err = a.policy.AddPolicy(req.Params...)
	} else {
		added, err = a.policy.AddGroupingPolicy(req.Params...)
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"added": added})
}

func (a *App) removePolicy(c *gin.Context) {
	var req policyRuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.PType != "p" && req.PType != "g" {
		fail(c, http.StatusBadRequest, "pType must be 'p' or 'g'")
		return
	}

	var removed bool
	var err error
	if req.PType == "p" {
		removed, err = a.policy.RemovePolicy(req.Params...)
	} else {
		removed, err = a.policy.RemoveGroupingPolicy(req.Params...)
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"removed": removed})
}

func (a *App) testPolicy(c *gin.Context) {
	var req struct {
		UserID   uint   `json:"userId"`
		Username string `json:"username"`
		Role     string `json:"role"`
		Resource string `json:"resource"`
		Action   string `json:"action"`
		Domain   string `json:"domain"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}

	allowed, err := a.policy.CheckPermissionForUser(req.UserID, req.Username, req.Role, req.Resource, req.Action, req.Domain)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"allowed": allowed})
}

func (a *App) listUserRoles(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var u model.User
	if err := a.db.First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, "user not found")
		return
	}
	roles, err := a.policy.GetImplicitRolesForUser(fmt.Sprintf("user:%d", id))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"userId": id, "username": u.Username, "roles": roles})
}

// SeedDefaultPolicies reseeds the default RBAC-compatible policies.
func (a *App) seedDefaultPolicies(c *gin.Context) {
	if err := a.policy.ResetDefaultPolicies(); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"message": "default policies seeded"})
}
