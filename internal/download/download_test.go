package download

import (
	"path/filepath"
	"testing"
)

func TestResolveDestination(t *testing.T) {
	base := t.TempDir()
	got, err := ResolveDestination(base, "models", "model.bin", "https://example.com/model.bin")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "models", "model.bin")
	if got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}

func TestResolveDestinationRejectsEscape(t *testing.T) {
	base := t.TempDir()
	if _, err := ResolveDestination(base, "..", "outside.bin", "https://example.com/model.bin"); err == nil {
		t.Fatal("expected destination outside download_dir to be rejected")
	}
}

func TestResolveDestinationUsesURLName(t *testing.T) {
	base := t.TempDir()
	got, err := ResolveDestination(base, "", "", "https://example.com/files/model.bin?version=2")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "model.bin")
	if got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}
