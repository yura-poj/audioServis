package media

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMP3Corpus(t *testing.T) {
	const dir = "../../testdata/mp3"
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		name, expected, ok := strings.Cut(line, " ")
		if !ok || expected != "true" && expected != "false" {
			t.Fatalf("invalid manifest entry %q", line)
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateMP3(path, info.Size())
			if expected == "true" && err != nil || expected == "false" && !errors.Is(err, ErrInvalidMP3) {
				t.Fatalf("expected validity %s, got %v", expected, err)
			}
		})
	}
}

func TestMP3SizeBoundary(t *testing.T) {
	// A bounded ID3 padding block followed by one complete MPEG-1 Layer III frame.
	frame := make([]byte, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0})
	padding := MaxSize - len(frame) - 10
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(padding>>21) & 127, byte(padding>>14) & 127, byte(padding>>7) & 127, byte(padding) & 127}
	file, err := os.CreateTemp(t.TempDir(), "boundary")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(header); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt(frame, MaxSize-int64(len(frame))); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMP3(file.Name(), MaxSize); err != nil {
		t.Fatal(err)
	}
	if err := validateMP3(bytes.NewReader(nil), MaxSize+1); !errors.Is(err, ErrInvalidMP3) {
		t.Fatal(err)
	}
}
