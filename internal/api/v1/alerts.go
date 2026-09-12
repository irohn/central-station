package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"central-station/internal/alerts"
	"central-station/internal/announce"
	"central-station/internal/auth"
	"central-station/internal/clips"
	"central-station/internal/httpx"
	"central-station/internal/player"
	"central-station/internal/zone"
)

func (a *api) requireAlerts() error {
	if a.Alerts == nil {
		return httpx.NewError(http.StatusServiceUnavailable, "alerts_unavailable", "alerts are not available on this server")
	}
	return nil
}

func (a *api) withNext(list []*alerts.Alert) []*alerts.Alert {
	now := time.Now()
	for _, al := range list {
		al.NextFireAt = alerts.NextFire(al, now)
	}
	return list
}

// GET /alerts
func (a *api) listAlerts(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireAlerts(); err != nil {
		return err
	}
	list, err := a.Alerts.List(r.Context())
	if err != nil {
		return err
	}
	if list == nil {
		list = []*alerts.Alert{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"alerts":          a.withNext(list),
		"defaultTimezone": a.Config.AlertsTimezone,
		"maxLoops":        alerts.MaxLoops,
		"types":           []string{alerts.TypeCron},
	})
	return nil
}

// alertInput is the body of POST /alerts and PATCH /alerts/{id}; pointers so
// PATCH can leave fields untouched.
type alertInput struct {
	Name     *string `json:"name"`
	Enabled  *bool   `json:"enabled"`
	Type     *string `json:"type"`
	Schedule *string `json:"schedule"`
	Timezone *string `json:"timezone"`
	ClipID   *string `json:"clipId"`
	Loops    *int    `json:"loops"`
	ChimeID  *string `json:"chimeId"`
	ZoneID   *string `json:"zoneId"` // "" = every zone
}

func (in *alertInput) apply(al *alerts.Alert) {
	if in.Name != nil {
		al.Name = *in.Name
	}
	if in.Enabled != nil {
		al.Enabled = *in.Enabled
	}
	if in.Type != nil {
		al.Type = *in.Type
	}
	if in.Schedule != nil {
		al.Schedule = *in.Schedule
	}
	if in.Timezone != nil {
		al.Timezone = *in.Timezone
	}
	if in.ClipID != nil {
		al.ClipID = *in.ClipID
	}
	if in.Loops != nil {
		al.Loops = *in.Loops
	}
	if in.ChimeID != nil {
		al.ChimeID = *in.ChimeID
	}
	if in.ZoneID != nil {
		al.ZoneID = strings.TrimSpace(*in.ZoneID)
	}
}

// checkRefs validates the clip, chime and zone references.
func (a *api) checkAlertRefs(r *http.Request, al *alerts.Alert) error {
	if al.ZoneID != "" {
		if _, ok := a.Zones.Get(al.ZoneID); !ok {
			return httpx.NotFound("zone_not_found", "zone %s not found", al.ZoneID)
		}
	}
	if _, err := a.Clips.Get(r.Context(), al.ClipID); errors.Is(err, clips.ErrNotFound) {
		return httpx.NotFound("clip_not_found", "clip %s not found", al.ClipID)
	} else if err != nil {
		return err
	}
	al.ChimeID = strings.TrimSpace(al.ChimeID)
	if al.ChimeID != "" && al.ChimeID != announce.ChimeNone {
		ch, err := a.Clips.Get(r.Context(), al.ChimeID)
		if errors.Is(err, clips.ErrNotFound) {
			return httpx.NotFound("clip_not_found", "chime %s not found", al.ChimeID)
		} else if err != nil {
			return err
		}
		if ch.Kind != clips.KindChime {
			return httpx.BadRequest("not_a_chime", "clip %q is not a chime", ch.Name)
		}
	}
	return nil
}

func alertError(err error) error {
	var ve *alerts.ValidationError
	switch {
	case errors.As(err, &ve):
		return httpx.BadRequest("invalid_alert", "%s", ve.Msg)
	case errors.Is(err, alerts.ErrNotFound):
		return httpx.NotFound("alert_not_found", "%s", err.Error())
	}
	return err
}

// POST /alerts
func (a *api) createAlert(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireAlerts(); err != nil {
		return err
	}
	u := auth.MustUser(r)
	var in alertInput
	if err := httpx.Decode(r, &in, false); err != nil {
		return err
	}
	now := time.Now()
	al := &alerts.Alert{ID: uuid.NewString(), Enabled: true, Type: alerts.TypeCron, Timezone: a.Config.AlertsTimezone, Loops: 1,
		CreatedByID: u.ID, CreatedByName: u.Name, CreatedAt: now, UpdatedAt: now}
	in.apply(al)
	if err := al.Validate(); err != nil {
		return alertError(err)
	}
	if err := a.checkAlertRefs(r, al); err != nil {
		return err
	}
	if err := a.Alerts.Insert(r.Context(), al); err != nil {
		return err
	}
	al.NextFireAt = alerts.NextFire(al, now)
	httpx.JSON(w, http.StatusCreated, al)
	return nil
}

// PATCH /alerts/{id}
func (a *api) updateAlert(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireAlerts(); err != nil {
		return err
	}
	var in alertInput
	if err := httpx.Decode(r, &in, false); err != nil {
		return err
	}
	al, err := a.Alerts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return alertError(err)
	}
	in.apply(al)
	if err := al.Validate(); err != nil {
		return alertError(err)
	}
	if err := a.checkAlertRefs(r, al); err != nil {
		return err
	}
	al.UpdatedAt = time.Now()
	if err := a.Alerts.Update(r.Context(), al); err != nil {
		return alertError(err)
	}
	al.NextFireAt = alerts.NextFire(al, al.UpdatedAt)
	httpx.JSON(w, http.StatusOK, al)
	return nil
}

// DELETE /alerts/{id}
func (a *api) deleteAlert(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireAlerts(); err != nil {
		return err
	}
	if err := a.Alerts.Delete(r.Context(), r.PathValue("id")); err != nil {
		return alertError(err)
	}
	httpx.NoContent(w)
	return nil
}

// POST /alerts/{id}/test  fires the alert right now (does not touch its schedule)
func (a *api) testAlert(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireAlerts(); err != nil {
		return err
	}
	u := auth.MustUser(r)
	al, err := a.Alerts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return alertError(err)
	}
	b, err := announce.Build(r.Context(), a.Clips, a.defaultChimeID(r.Context()), al.ClipID, al.Loops, al.ChimeID, u.ID, u.Name+" (test: "+al.Name+")")
	if err != nil {
		return announceError(err)
	}
	delivered, err := a.Zones.SendBroadcast(al.ZoneID, b)
	if errors.Is(err, player.ErrPlayerOffline) {
		return httpx.Conflict("player_offline", "no player is connected, so nothing would be heard")
	}
	if errors.Is(err, zone.ErrNotFound) {
		return httpx.NotFound("zone_not_found", "the alert's zone no longer exists")
	}
	if err != nil {
		return err
	}
	names := make([]string, 0, len(delivered))
	for _, d := range delivered {
		names = append(names, d.Zone.Name)
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"id": b.ID, "zones": names})
	return nil
}
