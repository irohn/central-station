// Package tts is the client for the text-to-speech service (tts/ in this repo).
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxChars bounds announcement text.
const MaxChars = 600

var ErrDisabled = errors.New("text-to-speech is not configured")

// Client talks to the TTS service.
type Client struct {
	URL  string // e.g. http://tts:5000; empty disables
	HTTP *http.Client
}

// New returns a client; url may be empty.
func New(url string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), HTTP: &http.Client{Timeout: 90 * time.Second}}
}

// Enabled reports whether a service URL is configured.
func (c *Client) Enabled() bool { return c != nil && c.URL != "" }

// Synthesize returns WAV audio for text.
func (c *Client) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/synthesize", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts service: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("tts service returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return io.ReadAll(io.LimitReader(res.Body, 20<<20))
}

// WAVDuration reads the duration of a 16-bit PCM WAV (0 if unparseable).
func WAVDuration(b []byte) float64 {
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0
	}
	le := func(i int) uint32 { return uint32(b[i]) | uint32(b[i+1])<<8 | uint32(b[i+2])<<16 | uint32(b[i+3])<<24 }
	// Walk chunks: fmt gives byte rate, data gives size.
	var byteRate uint32
	for off := 12; off+8 <= len(b); {
		id, size := string(b[off:off+4]), int(le(off+4))
		if id == "fmt " && off+8+12 <= len(b) {
			byteRate = le(off + 8 + 8)
		}
		if id == "data" {
			if byteRate == 0 {
				return 0
			}
			if off+8+size > len(b) {
				size = len(b) - off - 8
			}
			return float64(size) / float64(byteRate)
		}
		off += 8 + size + size%2
	}
	return 0
}
