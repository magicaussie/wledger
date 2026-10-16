package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/settings"
)

func TestGeneralSettingsSavePreservesLegacyLEDColorsBeforeCentralAdoption(t *testing.T) {
	f := newLEDHTTPFixture(t)
	f.h.Settings = settings.NewService(f.h.Queries)
	if err := f.h.Queries.InitSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(`UPDATE settings SET color_locate='#123456', color_error='#654321'`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/settings", strings.NewReader("require_auth=on&locate_timeout=30&color_ok=%2300FF00&color_low=%23FFFF00&color_critical=%23FF0000"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	f.h.HandleSettingsUpdate(response, request)
	if response.Code != 303 {
		t.Fatalf("save=%d %s", response.Code, response.Body.String())
	}
	saved, err := f.h.Queries.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if saved.ColorLocate.String != "#123456" || saved.ColorError.String != "#654321" {
		t.Fatalf("unrelated save changed LED colors: %+v", saved)
	}
}
