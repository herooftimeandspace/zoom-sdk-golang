package main

import (
	"strings"
	"testing"
)

// TestBumpVersion covers every supported release type and invalid input so a
// workflow change cannot silently emit an incorrect Go module tag.
func TestBumpVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		part    string
		want    string
		wantErr string
	}{
		{name: "patch", current: "1.2.3", part: "patch", want: "1.2.4"},
		{name: "minor", current: "v1.2.3", part: "minor", want: "1.3.0"},
		{name: "major", current: "1.2.3", part: "major", want: "2.0.0"},
		{name: "invalid version", current: "1.2", part: "patch", wantErr: "unsupported version format"},
		{name: "invalid part", current: "1.2.3", part: "build", wantErr: "unsupported semver part"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := bumpVersion(test.current, test.part)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("bumpVersion() error = %v, want error containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("bumpVersion() unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("bumpVersion() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestSelectSemverLabel verifies the default, explicit, duplicate, and
// ambiguous-label paths used when promotion pull requests are refreshed.
func TestSelectSemverLabel(t *testing.T) {
	tests := []struct {
		name    string
		labels  []string
		want    string
		wantErr bool
	}{
		{name: "default patch", labels: []string{"enhancement"}, want: "semver:patch"},
		{name: "explicit minor", labels: []string{"semver:minor"}, want: "semver:minor"},
		{name: "duplicate ignored", labels: []string{"semver:major", "semver:major"}, want: "semver:major"},
		{name: "ambiguous", labels: []string{"semver:patch", "semver:minor"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectSemverLabel(test.labels, "semver:patch")
			if test.wantErr {
				if err == nil {
					t.Fatal("selectSemverLabel() expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("selectSemverLabel() unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("selectSemverLabel() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestResolvePromotionSemverLabel confirms that the resolver prefers the pull
// request whose base matches the promotion source branch.
func TestResolvePromotionSemverLabel(t *testing.T) {
	feature := associatedPull{Number: 10}
	feature.Base.Ref = "dev"
	feature.Labels = append(feature.Labels, struct {
		Name string `json:"name"`
	}{Name: "semver:minor"})

	maintenance := associatedPull{Number: 11}
	maintenance.Base.Ref = "main"
	maintenance.Labels = append(maintenance.Labels, struct {
		Name string `json:"name"`
	}{Name: "semver:patch"})

	number, label, err := resolvePromotionSemverLabel([]associatedPull{maintenance, feature}, "dev", "semver:patch")
	if err != nil {
		t.Fatalf("resolvePromotionSemverLabel() unexpected error: %v", err)
	}
	if number != 10 || label != "semver:minor" {
		t.Fatalf("resolvePromotionSemverLabel() = (%d, %q), want (10, %q)", number, label, "semver:minor")
	}
}
