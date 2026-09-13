package v1

import (
	"errors"
	"net/http"
	"strings"

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
		a.Overrides.Apply(u, u.ID)
		out = append(out, toUserDTO(u))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out})
	return nil
}

// MaxDisplayName bounds display names.
const MaxDisplayName = 60

// PUT /users/{id}/permissions  {"permissions": ["queue:add", ...]}  (admins)
//
// Replaces the user's permissions for this API instance only; a restart
// returns everyone to their role. Admins cannot override themselves.
func (a *api) setUserPermissions(w http.ResponseWriter, r *http.Request) error {
	me := auth.MustUser(r)
	id := r.PathValue("id")
	if id == me.ID {
		return httpx.Conflict("cannot_change_own_permissions", "you cannot change your own permissions")
	}
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if body.Permissions == nil {
		return httpx.BadRequest("invalid_input", "permissions is required")
	}
	perms := make([]rbac.Permission, 0, len(body.Permissions))
	seen := map[rbac.Permission]bool{}
	for _, p := range body.Permissions {
		parsed, err := rbac.ParsePermission(p)
		if err != nil {
			return httpx.BadRequest("invalid_permission", "%s", err.Error())
		}
		if !seen[parsed] {
			seen[parsed] = true
			perms = append(perms, parsed)
		}
	}
	u, err := a.Users.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.NotFound("user_not_found", "user %s not found", id)
	}
	if err != nil {
		return err
	}
	a.Overrides.Set(id, perms)
	a.Overrides.Apply(u, id)
	httpx.JSON(w, http.StatusOK, toUserDTO(u))
	return nil
}

// DELETE /users/{id}/permissions  (admins) returns the user to their role's permissions.
func (a *api) clearUserPermissions(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	u, err := a.Users.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.NotFound("user_not_found", "user %s not found", id)
	}
	if err != nil {
		return err
	}
	a.Overrides.Clear(id)
	httpx.JSON(w, http.StatusOK, toUserDTO(u))
	return nil
}

// PATCH /users/{id}  {"name": "..."} | {"role": "collaborator"} | {"emailVerified": true}
//
// Anyone may change their own display name ("" restores the profile name);
// changing someone else's name, roles and verification need users:manage.
// Verifying applies the same promotion rules as the email link; unverifying
// only clears the flag (roles are never lowered automatically).
func (a *api) updateUser(w http.ResponseWriter, r *http.Request) error {
	me := auth.MustUser(r)
	id := r.PathValue("id")
	var body struct {
		Name          *string `json:"name"`
		Role          *string `json:"role"`
		EmailVerified *bool   `json:"emailVerified"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if body.Name == nil && body.Role == nil && body.EmailVerified == nil {
		return httpx.BadRequest("invalid_input", "name, role or emailVerified is required")
	}
	admin := me.Has(rbac.UsersManage)
	if !admin && (id != me.ID || body.Role != nil || body.EmailVerified != nil) {
		return httpx.Forbidden("role %q lacks permission %q", me.Role, rbac.UsersManage)
	}
	u, err := a.Users.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.NotFound("user_not_found", "user %s not found", id)
	}
	if err != nil {
		return err
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if len(name) > MaxDisplayName {
			return httpx.BadRequest("invalid_name", "display name must be at most %d characters", MaxDisplayName)
		}
		if u, err = a.Users.SetDisplayName(r.Context(), id, name); err != nil {
			return err
		}
	}
	if body.EmailVerified != nil && *body.EmailVerified != u.EmailVerified {
		if *body.EmailVerified {
			if u, err = a.markVerified(r.Context(), u); err != nil {
				return err
			}
		} else {
			if u, err = a.Users.SetVerified(r.Context(), id, false); err != nil {
				return err
			}
		}
	}
	if body.Role != nil {
		role, err := rbac.ParseRole(*body.Role)
		if err != nil {
			return httpx.BadRequest("invalid_role", "%s", err.Error())
		}
		if id == me.ID && role != me.Role {
			return httpx.Conflict("cannot_change_own_role", "you cannot change your own role")
		}
		if role != u.Role {
			if u, err = a.Users.SetRole(r.Context(), id, role); err != nil {
				return err
			}
		}
	}
	a.Overrides.Apply(u, u.ID)
	httpx.JSON(w, http.StatusOK, toUserDTO(u))
	return nil
}
