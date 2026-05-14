package zoomsdk

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Settings contains normalized runtime configuration.
type Settings struct {
	AccountID          string
	ClientID           string
	ClientSecret       string
	BaseURL            string
	OAuthURL           string
	TokenSkewSeconds   int
	MaxRetries         int
	BackoffBaseSeconds float64
	BackoffMaxSeconds  float64
	TimeoutSeconds     float64
}

// DefaultSettings returns the default runtime settings.
func DefaultSettings() Settings {
	return Settings{
		BaseURL:            "https://api.zoom.us/v2",
		OAuthURL:           "https://zoom.us",
		TokenSkewSeconds:   60,
		MaxRetries:         3,
		BackoffBaseSeconds: 0.5,
		BackoffMaxSeconds:  8.0,
		TimeoutSeconds:     30.0,
	}
}

// LoadSettingsFromEnvironment builds settings from environment variables and an optional .env file.
func LoadSettingsFromEnvironment(loadLocalEnv bool) (Settings, error) {
	settings := DefaultSettings()
	dotenvValues := map[string]string{}
	if loadLocalEnv {
		root := discoverProjectRoot(".")
		_ = loadDotenvFile(filepath.Join(root, ".env"), dotenvValues)
	}

	envValue := func(key, fallback string) string {
		if value, ok := os.LookupEnv(key); ok {
			return strings.TrimSpace(value)
		}
		if value, ok := dotenvValues[key]; ok {
			return value
		}
		return fallback
	}

	settings.AccountID = envValue("ZOOM_ACCOUNT_ID", "")
	settings.ClientID = envValue("ZOOM_CLIENT_ID", "")
	settings.ClientSecret = envValue("ZOOM_CLIENT_SECRET", "")
	settings.BaseURL = envValue("ZOOM_BASE_URL", settings.BaseURL)
	settings.OAuthURL = envValue("ZOOM_OAUTH_URL", settings.OAuthURL)

	rawTokenSkew := envValue("ZOOM_TOKEN_SKEW_SECONDS", strconv.Itoa(settings.TokenSkewSeconds))
	tokenSkew, err := strconv.Atoi(rawTokenSkew)
	if err != nil {
		return Settings{}, &ConfigError{Field: "ZOOM_TOKEN_SKEW_SECONDS", Message: "must be an integer"}
	}
	settings.TokenSkewSeconds = tokenSkew

	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// Validate enforces the runtime setting invariants.
func (s *Settings) Validate() error {
	baseURL, err := validateHTTPSURL(s.BaseURL, "base_url")
	if err != nil {
		return err
	}
	oauthURL, err := validateHTTPSURL(s.OAuthURL, "oauth_url")
	if err != nil {
		return err
	}
	if s.TokenSkewSeconds < 0 {
		return &ConfigError{Field: "token_skew_seconds", Message: "must be greater than or equal to 0"}
	}
	if s.MaxRetries < 0 {
		return &ConfigError{Field: "max_retries", Message: "must be greater than or equal to 0"}
	}
	if s.BackoffBaseSeconds <= 0 {
		return &ConfigError{Field: "backoff_base_seconds", Message: "must be greater than 0"}
	}
	if s.BackoffMaxSeconds <= 0 {
		return &ConfigError{Field: "backoff_max_seconds", Message: "must be greater than 0"}
	}
	if s.TimeoutSeconds <= 0 {
		return &ConfigError{Field: "timeout_seconds", Message: "must be greater than 0"}
	}
	s.BaseURL = baseURL
	s.OAuthURL = oauthURL
	return nil
}

func validateHTTPSURL(raw string, field string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", &ConfigError{Field: field, Message: "must be a valid URL"}
	}
	if parsed.Scheme != "https" {
		return "", &ConfigError{Field: field, Message: "must use https"}
	}
	if parsed.Host == "" {
		return "", &ConfigError{Field: field, Message: "must include a hostname"}
	}
	if parsed.User != nil {
		return "", &ConfigError{Field: field, Message: "must not include embedded credentials"}
	}
	if parsed.RawQuery != "" {
		return "", &ConfigError{Field: field, Message: "must not include a query string"}
	}
	if parsed.Fragment != "" {
		return "", &ConfigError{Field: field, Message: "must not include a fragment"}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func discoverProjectRoot(start string) string {
	current, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		current = parent
	}
}

func loadDotenvFile(path string, values map[string]string) error {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			values[key] = value
		}
	}
	return scanner.Err()
}
