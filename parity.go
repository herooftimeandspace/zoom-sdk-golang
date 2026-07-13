package zoomsdk

import (
	"embed"
	"encoding/json"
)

//go:embed internal/parity/golden/sdk_public_surface.json internal/parity/schemas
var parityAssets embed.FS

// GoldenPublicSurface loads the vendored SDK golden surface snapshot.
func GoldenPublicSurface() (map[string]map[string]any, error) {
	payload, err := parityAssets.ReadFile("internal/parity/golden/sdk_public_surface.json")
	if err != nil {
		return nil, err
	}
	var parsed map[string]map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}
