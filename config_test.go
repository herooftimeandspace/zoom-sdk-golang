package zoomsdk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsValidateRejectsInsecureURL(t *testing.T) {
	settings := DefaultSettings()
	settings.BaseURL = "http://api.zoom.us/v2"
	if err := settings.Validate(); err == nil {
		t.Fatal("expected insecure base URL to fail validation")
	}
}

func TestSettingsValidateRejectsEmbeddedCredentials(t *testing.T) {
	settings := DefaultSettings()
	settings.OAuthURL = "https://user:pass@zoom.us"
	if err := settings.Validate(); err == nil {
		t.Fatal("expected embedded credentials to fail validation")
	}
}

func TestLoadSettingsFromEnvironmentParsesValues(t *testing.T) {
	t.Setenv("ZOOM_BASE_URL", "https://api.zoom.us/v2")
	t.Setenv("ZOOM_OAUTH_URL", "https://zoom.us")
	t.Setenv("ZOOM_TOKEN_SKEW_SECONDS", "120")

	settings, err := LoadSettingsFromEnvironment(false)
	if err != nil {
		t.Fatalf("unexpected settings load error: %v", err)
	}
	if settings.TokenSkewSeconds != 120 {
		t.Fatalf("unexpected token skew: %d", settings.TokenSkewSeconds)
	}
}

func TestLoadDotenvFileReadsValues(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".env")
	if err := os.WriteFile(path, []byte("ZOOM_ACCOUNT_ID=acct-123\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	values := map[string]string{}
	if err := loadDotenvFile(path, values); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	if values["ZOOM_ACCOUNT_ID"] != "acct-123" {
		t.Fatalf("unexpected dotenv value: %#v", values)
	}
}
