package main

import (
	"slices"
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

// TestResolvePromotionRangeSemverLabel verifies that a staging promotion uses
// the highest-impact label from every source pull request in the unpromoted
// commit range. Repeated pull requests model the same source PR being
// associated with more than one commit and must not change the result.
func TestResolvePromotionRangeSemverLabel(t *testing.T) {
	tests := []struct {
		name        string
		commitPulls [][]associatedPull
		want        string
		wantPRs     []int
		wantErr     bool
	}{
		{
			name: "patch only",
			commitPulls: [][]associatedPull{
				{testAssociatedPull(20, "dev", "semver:patch")},
			},
			want:    "semver:patch",
			wantPRs: []int{20},
		},
		{
			name: "minor outranks later patch",
			commitPulls: [][]associatedPull{
				{testAssociatedPull(21, "dev", "semver:minor")},
				{testAssociatedPull(22, "dev", "semver:patch")},
			},
			want:    "semver:minor",
			wantPRs: []int{21, 22},
		},
		{
			name: "major outranks later minor and patch on refresh",
			commitPulls: [][]associatedPull{
				{testAssociatedPull(23, "dev", "semver:major")},
				{testAssociatedPull(23, "dev", "semver:major")},
				{testAssociatedPull(24, "dev", "semver:minor")},
				{testAssociatedPull(25, "dev", "semver:patch")},
			},
			want:    "semver:major",
			wantPRs: []int{23, 24, 25},
		},
		{
			name: "unlabeled source defaults to patch",
			commitPulls: [][]associatedPull{
				{testAssociatedPull(26, "dev")},
			},
			want:    "semver:patch",
			wantPRs: []int{26},
		},
		{
			name:        "unassociated commits default to patch",
			commitPulls: [][]associatedPull{{}},
			want:        "semver:patch",
			wantPRs:     []int{},
		},
		{
			name: "preferred source base wins",
			commitPulls: [][]associatedPull{
				{
					testAssociatedPull(27, "staging", "semver:patch"),
					testAssociatedPull(28, "dev", "semver:minor"),
				},
			},
			want:    "semver:minor",
			wantPRs: []int{28},
		},
		{
			name: "ambiguous source labels fail closed",
			commitPulls: [][]associatedPull{
				{testAssociatedPull(29, "dev", "semver:patch", "semver:minor")},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, prs, err := resolvePromotionRangeSemverLabel(test.commitPulls, "dev", "semver:patch")
			if test.wantErr {
				if err == nil {
					t.Fatal("resolvePromotionRangeSemverLabel() expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePromotionRangeSemverLabel() unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("resolvePromotionRangeSemverLabel() label = %q, want %q", got, test.want)
			}
			if !slices.Equal(prs, test.wantPRs) {
				t.Fatalf("resolvePromotionRangeSemverLabel() PRs = %v, want %v", prs, test.wantPRs)
			}
		})
	}
}

// testAssociatedPull builds the minimal GitHub response shape needed by the
// release-label resolvers while keeping individual test cases readable.
func testAssociatedPull(number int, base string, labels ...string) associatedPull {
	pull := associatedPull{Number: number}
	pull.Base.Ref = base
	for _, label := range labels {
		pull.Labels = append(pull.Labels, struct {
			Name string `json:"name"`
		}{Name: label})
	}
	return pull
}
