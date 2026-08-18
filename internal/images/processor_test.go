package images

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/disintegration/imaging"
	"github.com/tuxedocurly/wledger/internal/config"
)

func TestProcessBytesWebP(t *testing.T) {
	if err := os.MkdirAll(config.DirUploadsImages, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Download a real webp fixture (Altronics serves webp). Skip if offline.
	data, err := fetchWebPFixture()
	if err != nil {
		t.Skipf("could not fetch webp fixture: %v", err)
	}

	name, err := ProcessBytes(data)
	if err != nil {
		t.Fatalf("ProcessBytes failed on webp: %v", err)
	}
	if name == "" {
		t.Fatal("expected a filename")
	}

	// Verify the saved main image decodes.
	full := filepath.Join(config.DirUploadsImages, name)
	f, err := os.Open(full)
	if err != nil {
		t.Fatalf("failed to open saved image: %v", err)
	}
	defer f.Close()
	if _, err := imaging.Decode(f); err != nil {
		t.Errorf("saved image failed to decode: %v", err)
	}
}

func fetchWebPFixture() ([]byte, error) {
	resp, err := http.Get("https://assets.altronics.com.au/0044635_z0568-lm340t-5v-1a-regulator-to-220-main_510.webp")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
