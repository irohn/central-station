package playerd

import (
	"testing"

	"central-station/internal/player"
)

func TestChooseDevice(t *testing.T) {
	devs := []player.AudioDevice{{ID: "pulse/usb", Name: "USB"}, {ID: "pulse/bt", Name: "Bluetooth"}}
	cases := []struct {
		req     string
		want    string
		missing bool
	}{
		{"", "auto", false},
		{"auto", "auto", false},
		{"pulse/bt", "pulse/bt", false},
		{"pulse/gone", "auto", true},
	}
	for _, c := range cases {
		got, missing := chooseDevice(c.req, devs)
		if got != c.want || missing != c.missing {
			t.Errorf("chooseDevice(%q) = %q,%v want %q,%v", c.req, got, missing, c.want, c.missing)
		}
	}
	if got, missing := chooseDevice("pulse/usb", nil); got != "auto" || !missing {
		t.Errorf("empty list: %q %v", got, missing)
	}
}

func TestBroadcastAudioArgsUsesSelectedDevice(t *testing.T) {
	a := &Agent{AudioArgs: []string{"--ao=pulse"}, devices: []player.AudioDevice{{ID: "pulse/bt"}}}
	if got := a.broadcastAudioArgs(); len(got) != 1 {
		t.Fatalf("auto: %v", got)
	}
	a.requestedDevice = "pulse/bt"
	if got := a.broadcastAudioArgs(); len(got) != 2 || got[1] != "--audio-device=pulse/bt" {
		t.Fatalf("selected: %v", got)
	}
	a.requestedDevice = "pulse/gone"
	if got := a.broadcastAudioArgs(); len(got) != 1 {
		t.Fatalf("missing falls back to defaults: %v", got)
	}
}

func TestFilterDevices(t *testing.T) {
	got := filterDevices([]AudioDevice{
		{Name: "auto", Description: "Autoselect device"},
		{Name: "pulse/RDPSink", Description: "RDP Sink"},
		{Name: "alsa", Description: "Default (alsa)"},
		{Name: "jack", Description: "Default (jack)"},
		{Name: "coreaudio/AppleHDA", Description: "MacBook Pro Speakers"},
	})
	if len(got) != 2 || got[0].ID != "pulse/RDPSink" || got[1].Name != "MacBook Pro Speakers" {
		t.Fatalf("filtered: %+v", got)
	}
	if !devicesEqual(got, got) || devicesEqual(got, got[:1]) {
		t.Fatal("devicesEqual")
	}
}

func TestIsOutputError(t *testing.T) {
	for msg, want := range map[string]bool{
		"audio output initialization failed": true,
		"Audio device not found":             true,
		"loading failed":                     false,
		"Failed to recognize file format":    false,
		"":                                   false,
	} {
		if got := isOutputError(msg); got != want {
			t.Errorf("isOutputError(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestResolveTargetUsesConfiguredDefault(t *testing.T) {
	a := &Agent{}
	if got := a.resolveTarget("auto"); got != "auto" {
		t.Fatalf("no default: %q", got)
	}
	a.DefaultDevice = "alsa/default:CARD=C5201"
	if got := a.resolveTarget("auto"); got != "alsa/default:CARD=C5201" {
		t.Fatalf("auto should map to the configured device, got %q", got)
	}
	if got := a.resolveTarget("pulse/bt"); got != "pulse/bt" {
		t.Fatalf("explicit choice must win, got %q", got)
	}
}
