package v1

import (
	"errors"
	"net/http"

	"central-station/internal/httpx"
	"central-station/internal/zone"
)

func zoneError(err error) error {
	var ve *zone.ValidationError
	switch {
	case errors.As(err, &ve):
		return httpx.BadRequest("invalid_zone", "%s", ve.Msg)
	case errors.Is(err, zone.ErrNotFound):
		return httpx.NotFound("zone_not_found", "%s", err.Error())
	case errors.Is(err, zone.ErrLastZone):
		return httpx.Conflict("last_zone", "%s", err.Error())
	}
	return err
}

// GET /zones  (any signed-in user): every zone with its player and playback summary.
func (a *api) listZones(w http.ResponseWriter, _ *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{"zones": a.Zones.Summaries(), "defaultId": zone.DefaultID})
	return nil
}

// GET /zones/{zone}
func (a *api) getZone(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	for _, s := range a.Zones.Summaries() {
		if s.ID == rt.Zone.ID {
			httpx.JSON(w, http.StatusOK, s)
			return nil
		}
	}
	return httpx.NotFound("zone_not_found", "zone %s not found", rt.Zone.ID)
}

// POST /zones  {"name": "Kitchen"}  (admins)
//
// The response carries the zone's player token exactly once; put it in the
// player container's PLAYER_TOKEN.
func (a *api) createZone(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	rt, token, err := a.Zones.Create(r.Context(), body.Name)
	if err != nil {
		return zoneError(err)
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"zone": rt.Zone, "token": token})
	return nil
}

// PATCH /zones/{zone}  {"name": "..."}  (admins)
func (a *api) renameZone(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	z, err := a.Zones.Rename(r.Context(), r.PathValue("zone"), body.Name)
	if err != nil {
		return zoneError(err)
	}
	httpx.JSON(w, http.StatusOK, z)
	return nil
}

// DELETE /zones/{zone}  (admins) removes the zone and everything queued in it.
func (a *api) deleteZone(w http.ResponseWriter, r *http.Request) error {
	if err := a.Zones.Delete(r.Context(), r.PathValue("zone")); err != nil {
		return zoneError(err)
	}
	httpx.NoContent(w)
	return nil
}

// POST /zones/{zone}/token  (admins) issues a new player token; the old one stops working.
func (a *api) rotateZoneToken(w http.ResponseWriter, r *http.Request) error {
	token, err := a.Zones.RotateToken(r.Context(), r.PathValue("zone"))
	if err != nil {
		return zoneError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"token": token})
	return nil
}
