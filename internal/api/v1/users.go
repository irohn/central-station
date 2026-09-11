package v1

import (
	"errors"
	"net/http"

	"central-station/internal/auth"
	"central-station/internal/httpx"
	"central-station/internal/rbac"
	"central-station/internal/store"
)

// GET /roles
func (a *api) listRoles(w http.ResponseWriter, _ *http.Request) error {
	type roleDTO struct {
		Role        rbac.Role         `json:"role"`
		Level       int               `json:"level"`
		Permissions []rbac.Permission `json:"permissions"`
	}
	out := make([]roleDTO, 0, len(rbac.AllRoles))
	for _, r := range rbac.AllRoles {
		out = append(out, roleDTO{Role: r, Level: r.Level(), Permissions: r.Permissions()})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"roles": out, "permissions": rbac.AllPermissions, "grants": rbac.Grants()})
	return nil
}

// GET /users
func (a *api) listUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := a.Users.List(r.Context())
	if err != nil {
		return err
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, toUserDTO(u))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out})
	return nil
}

// PATCH /users/{id}  {"role": "collaborator"}
func (a *api) updateUser(w http.ResponseWriter, r *http.Request) error {
	me := auth.MustUser(r)
	id := r.PathValue("id")
	var body struct {
		Role string `json:"role"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	role, err := rbac.ParseRole(body.Role)
	if err != nil {
		return httpx.BadRequest("invalid_role", "%s", err.Error())
	}
	if id == me.ID && role != me.Role {
		return httpx.Conflict("cannot_change_own_role", "you cannot change your own role")
	}
	u, err := a.Users.SetRole(r.Context(), id, role)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.NotFound("user_not_found", "user %s not found", id)
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, toUserDTO(u))
	return nil
}
