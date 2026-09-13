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
