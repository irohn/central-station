// Package announce turns "play this clip N times behind a chime" into a
// player.Broadcast, shared by manual broadcasts and scheduled alerts.
package announce

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"central-station/internal/clips"
	"central-station/internal/player"
	"central-station/internal/settings"
)

// ErrNotAChime is returned when chimeID does not point at a chime clip.
var ErrNotAChime = errors.New("not a chime")

// ChimeNone means "no chime before the clip".
const ChimeNone = "none"

// Build resolves clipID and chimeID ("" = defaultChimeID, ChimeNone = none)
// into a broadcast attributed to byID/byName.
func Build(ctx context.Context, cs clips.Store, defaultChimeID, clipID string, loops int, chimeID, byID, byName string) (*player.Broadcast, error) {
	c, err := cs.Get(ctx, clipID)
	if err != nil {
		return nil, fmt.Errorf("clip: %w", err)
	}
	if loops < 1 {
		loops = 1
	}
	if loops > player.MaxBroadcastLoops {
		loops = player.MaxBroadcastLoops
	}
	b := &player.Broadcast{ID: uuid.NewString(), ClipID: c.ID, ClipName: c.Name, ClipURL: AudioPath(c.ID), Loops: loops, ByID: byID, ByName: byName}
	chimeID = strings.TrimSpace(chimeID)
	if chimeID == "" {
		chimeID = defaultChimeID
	}
	if chimeID != ChimeNone && chimeID != "" && c.Kind != clips.KindChime {
		ch, err := cs.Get(ctx, chimeID)
		if err != nil {
			return nil, fmt.Errorf("chime: %w", err)
		}
		if ch.Kind != clips.KindChime {
			return nil, fmt.Errorf("%w: %q", ErrNotAChime, ch.Name)
		}
		b.ChimeURL = AudioPath(ch.ID)
	}
	return b, nil
}

// ChimeSettingKey is the settings-store key holding the default chime id.
const ChimeSettingKey = "chime.default"

// DefaultChime returns the configured default chime id (bundled default if unset).
func DefaultChime(ctx context.Context, st settings.Store) string {
	if st != nil {
		if raw, err := st.Get(ctx, ChimeSettingKey); err == nil {
			if id := strings.Trim(string(raw), "\""); id != "" {
				return id
			}
		}
	}
	return clips.DefaultChimeID
}

// AudioPath is the API path the player fetches clip audio from.
func AudioPath(clipID string) string { return "/api/v1/player/clips/" + clipID + "/audio" }
