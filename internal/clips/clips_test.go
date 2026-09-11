package clips

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestBuiltinsAreValidWAV(t *testing.T) {
	for _, b := range Builtins() {
		if !bytes.HasPrefix(b.Audio, []byte("RIFF")) || !bytes.Contains(b.Audio[:44], []byte("WAVEfmt ")) {
			t.Fatalf("%s: not a WAV", b.Clip.Name)
		}
		if b.Clip.DurationSeconds < 0.5 || b.Clip.DurationSeconds > 3 || b.Clip.Size != len(b.Audio) || !b.Clip.Builtin || b.Clip.Kind != KindChime {
			t.Fatalf("%s: metadata %+v", b.Clip.Name, b.Clip)
		}
	}
}

func TestMemoryStoreAndBuiltins(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	if err := EnsureBuiltins(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBuiltins(ctx, s); err != nil { // idempotent
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	if len(list) != 3 || !list[0].Builtin {
		t.Fatalf("list = %d", len(list))
	}
	c := &Clip{ID: "11111111-1111-4111-8111-111111111111", Name: "lunch", Kind: KindRecording, Mime: "audio/mp4", Size: 3, DurationSeconds: 2.5, CreatedByID: "u1", CreatedByName: "Ann", CreatedAt: time.Now()}
	if err := s.Insert(ctx, c, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	got, audio, err := s.Audio(ctx, c.ID)
	if err != nil || got.Name != "lunch" || len(audio) != 3 {
		t.Fatalf("audio: %v %+v %v", err, got, audio)
	}
	if r, err := s.Rename(ctx, c.ID, "Lunch!"); err != nil || r.Name != "Lunch!" {
		t.Fatalf("rename: %v %+v", err, r)
	}
	if list, _ = s.List(ctx); len(list) != 4 || list[3].Name != "Lunch!" {
		t.Fatalf("list after insert: %+v", list)
	}
	if err := s.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, c.ID); err != ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
	if NormalizeMime("audio/webm;codecs=opus") != "audio/webm" || !AllowedMimes[NormalizeMime("Audio/MP4")] {
		t.Fatal("mime normalisation")
	}
}
