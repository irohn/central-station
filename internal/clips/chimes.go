package clips

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"
)

// Builtin pairs a bundled clip with its audio.
type Builtin struct {
	Clip  Clip
	Audio []byte
}

// Fixed ids so the rows are stable across restarts and deployments.
const (
	ChimeDingDongID = "c0000000-0000-4000-8000-000000000001"
	ChimeBellID     = "c0000000-0000-4000-8000-000000000002"
	ChimeThreeID    = "c0000000-0000-4000-8000-000000000003"
)

// DefaultChimeID is used when no default has been chosen.
const DefaultChimeID = ChimeDingDongID

const sampleRate = 44100

// Builtins returns the bundled chimes, synthesised on the fly (a few KB each).
func Builtins() []Builtin {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id, name string, samples []float64) Builtin {
		wav := encodeWAV(samples)
		return Builtin{
			Clip:  Clip{ID: id, Name: name, Kind: KindChime, Mime: "audio/wav", Size: len(wav), DurationSeconds: float64(len(samples)) / sampleRate, CreatedAt: at, Builtin: true},
			Audio: wav,
		}
	}
	return []Builtin{
		mk(ChimeDingDongID, "Ding-dong", concat(tone(659.25, 0.45, 0.9), tone(523.25, 0.8, 0.9))),
		mk(ChimeBellID, "Bell", tone(880, 1.3, 0.8)),
		mk(ChimeThreeID, "Three notes", concat(tone(523.25, 0.28, 0.8), tone(659.25, 0.28, 0.8), tone(783.99, 0.55, 0.8))),
	}
}

// tone is a decaying sine with a soft overtone, like a struck bar.
func tone(freq, seconds, gain float64) []float64 {
	n := int(seconds * sampleRate)
	out := make([]float64, n)
	for i := range out {
		t := float64(i) / sampleRate
		env := math.Exp(-4 * t / seconds)
		attack := math.Min(1, t/0.008)
		v := math.Sin(2*math.Pi*freq*t) + 0.25*math.Sin(2*math.Pi*freq*2*t)*math.Exp(-8*t/seconds)
		out[i] = gain * attack * env * v / 1.25
	}
	return out
}

func concat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// encodeWAV writes 16-bit mono PCM.
func encodeWAV(samples []float64) []byte {
	var pcm bytes.Buffer
	for _, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		binary.Write(&pcm, binary.LittleEndian, int16(s*32767)) //nolint:errcheck
	}
	data := pcm.Bytes()
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(data))) //nolint:errcheck
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))           //nolint:errcheck
	binary.Write(&b, binary.LittleEndian, uint16(1))            //nolint:errcheck  PCM
	binary.Write(&b, binary.LittleEndian, uint16(1))            //nolint:errcheck  mono
	binary.Write(&b, binary.LittleEndian, uint32(sampleRate))   //nolint:errcheck
	binary.Write(&b, binary.LittleEndian, uint32(sampleRate*2)) //nolint:errcheck
	binary.Write(&b, binary.LittleEndian, uint16(2))            //nolint:errcheck
	binary.Write(&b, binary.LittleEndian, uint16(16))           //nolint:errcheck
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(data))) //nolint:errcheck
	b.Write(data)
	return b.Bytes()
}
