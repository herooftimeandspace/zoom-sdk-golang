// Command release_tools implements the small, deterministic semantic-version
// operations used by the promotion and release workflows.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var validSemverLabels = []string{"semver:patch", "semver:minor", "semver:major"}
var semverLabelImpact = map[string]int{"semver:patch": 0, "semver:minor": 1, "semver:major": 2}
var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// associatedPull contains only the GitHub pull-request fields needed to carry
// a release label from source work into an automated promotion pull request.
type associatedPull struct {
	Number int `json:"number"`
	Base   struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// main dispatches the release helper subcommand requested by a workflow.
func main() {
	if len(os.Args) < 2 {
		fatal("usage: go run scripts/release_tools.go <bump|resolve-promotion-semver|resolve-promotion-range-semver> [flags]")
	}

	switch os.Args[1] {
	case "bump":
		runBump(os.Args[2:])
	case "resolve-promotion-semver":
		runResolvePromotionSemver(os.Args[2:])
	case "resolve-promotion-range-semver":
		runResolvePromotionRangeSemver(os.Args[2:])
	default:
		fatal("unsupported release tool command %q", os.Args[1])
	}
}

// runResolvePromotionRangeSemver resolves the highest release impact across
// every source pull request represented by commits in base..head. Unlike the
// single-commit resolver used for dev-to-staging promotion, this range-based
// command preserves an earlier minor or major impact when later lower-impact
// work refreshes an open staging-to-main promotion pull request.
func runResolvePromotionRangeSemver(args []string) {
	flags := flag.NewFlagSet("resolve-promotion-range-semver", flag.ExitOnError)
	repo := flags.String("repo", "", "GitHub owner/repository")
	base := flags.String("base", "", "base Git revision excluded from the promotion range")
	head := flags.String("head", "", "head Git revision included in the promotion range")
	sourceBaseRef := flags.String("source-base-ref", "", "preferred base branch for associated source pull requests")
	defaultLabel := flags.String("default-label", "semver:patch", "default semver label")
	if err := flags.Parse(args); err != nil {
		fatal("parse resolve-promotion-range-semver flags: %v", err)
	}
	if *repo == "" || *base == "" || *head == "" {
		fatal("--repo, --base, and --head are required")
	}

	commits, err := commitsBetween(*base, *head)
	if err != nil {
		fatal("resolve promotion commit range %s..%s: %v", *base, *head, err)
	}
	commitPulls := make([][]associatedPull, 0, len(commits))
	for _, commit := range commits {
		pulls, err := associatedPulls(*repo, commit)
		if err != nil {
			fatal("resolve pull requests associated with %s: %v", commit, err)
		}
		commitPulls = append(commitPulls, pulls)
	}

	label, prNumbers, err := resolvePromotionRangeSemverLabel(commitPulls, *sourceBaseRef, *defaultLabel)
	if err != nil {
		fatal("%v", err)
	}
	if len(prNumbers) == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "No associated source pull requests found in %s..%s; defaulting to %s.\n", *base, *head, label)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Resolved %s across source PRs %v in %s..%s.\n", label, prNumbers, *base, *head)
	}
	fmt.Println(label)
}

// runBump parses the bump command and prints the next semantic version.
func runBump(args []string) {
	flags := flag.NewFlagSet("bump", flag.ExitOnError)
	part := flags.String("part", "", "semantic-version part: patch, minor, or major")
	currentVersion := flags.String("current-version", "", "current semantic version")
	if err := flags.Parse(args); err != nil {
		fatal("parse bump flags: %v", err)
	}
	if *part == "" || *currentVersion == "" {
		fatal("--part and --current-version are required")
	}
	next, err := bumpVersion(*currentVersion, *part)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(next)
}

// runResolvePromotionSemver finds the source pull request for a commit and
// prints its single semver label, defaulting to a patch when none is present.
func runResolvePromotionSemver(args []string) {
	flags := flag.NewFlagSet("resolve-promotion-semver", flag.ExitOnError)
	repo := flags.String("repo", "", "GitHub owner/repository")
	sha := flags.String("sha", "", "commit SHA")
	baseRef := flags.String("base-ref", "", "preferred associated pull-request base branch")
	defaultLabel := flags.String("default-label", "semver:patch", "default semver label")
	if err := flags.Parse(args); err != nil {
		fatal("parse resolve-promotion-semver flags: %v", err)
	}
	if *repo == "" || *sha == "" {
		fatal("--repo and --sha are required")
	}

	pulls, err := associatedPulls(*repo, *sha)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Could not resolve pull requests associated with %s; defaulting to %s. Reason: %v\n", *sha, *defaultLabel, err)
		fmt.Println(*defaultLabel)
		return
	}

	prNumber, label, err := resolvePromotionSemverLabel(pulls, *baseRef, *defaultLabel)
	if err != nil {
		fatal("%v", err)
	}
	if prNumber == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "No associated pull request found for %s; defaulting to %s.\n", *sha, label)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Resolved %s from PR #%d.\n", label, prNumber)
	}
	fmt.Println(label)
}

// associatedPulls asks GitHub for pull requests associated with a commit. The
// gh CLI supplies authentication in Actions and in maintainer environments.
func associatedPulls(repo string, sha string) ([]associatedPull, error) {
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/commits/%s/pulls", repo, sha))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, errors.New(detail)
	}
	var pulls []associatedPull
	if err := json.Unmarshal(output, &pulls); err != nil {
		return nil, err
	}
	return pulls, nil
}

// commitsBetween returns commits reachable from head but not base in stable
// oldest-to-newest order. Arguments are passed directly to Git, never through
// a shell, so workflow-provided revision names cannot inject commands.
func commitsBetween(base string, head string) ([]string, error) {
	cmd := exec.Command("git", "rev-list", "--reverse", fmt.Sprintf("%s..%s", base, head))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, errors.New(detail)
	}
	return strings.Fields(string(output)), nil
}

// resolvePromotionSemverLabel prefers the pull request targeting baseRef and
// returns the release label that should be copied to the promotion pull request.
func resolvePromotionSemverLabel(pulls []associatedPull, baseRef string, defaultLabel string) (int, string, error) {
	if !slices.Contains(validSemverLabels, defaultLabel) {
		return 0, "", fmt.Errorf("unsupported default semver label %q", defaultLabel)
	}

	selected := preferredAssociatedPull(pulls, baseRef)
	if selected == nil {
		return 0, defaultLabel, nil
	}

	labels := make([]string, 0, len(selected.Labels))
	for _, label := range selected.Labels {
		labels = append(labels, label.Name)
	}
	resolved, err := selectSemverLabel(labels, defaultLabel)
	if err != nil {
		return 0, "", err
	}
	return selected.Number, resolved, nil
}

// resolvePromotionRangeSemverLabel selects one source pull request per commit,
// deduplicates pull requests that are associated with multiple commits, and
// returns the label with the greatest semantic-version impact. Each source PR
// must still carry at most one semver label; ambiguous labeling fails closed.
func resolvePromotionRangeSemverLabel(commitPulls [][]associatedPull, sourceBaseRef string, defaultLabel string) (string, []int, error) {
	if !slices.Contains(validSemverLabels, defaultLabel) {
		return "", nil, fmt.Errorf("unsupported default semver label %q", defaultLabel)
	}

	resolved := defaultLabel
	seenPulls := make(map[int]bool)
	prNumbers := make([]int, 0)
	for _, pulls := range commitPulls {
		selected := preferredAssociatedPull(pulls, sourceBaseRef)
		if selected == nil || seenPulls[selected.Number] {
			continue
		}
		seenPulls[selected.Number] = true
		prNumbers = append(prNumbers, selected.Number)

		labels := make([]string, 0, len(selected.Labels))
		for _, label := range selected.Labels {
			labels = append(labels, label.Name)
		}
		label, err := selectSemverLabel(labels, defaultLabel)
		if err != nil {
			return "", nil, fmt.Errorf("resolve semver label for PR #%d: %w", selected.Number, err)
		}
		if semverLabelImpact[label] > semverLabelImpact[resolved] {
			resolved = label
		}
	}
	return resolved, prNumbers, nil
}

// preferredAssociatedPull chooses the source PR targeting baseRef when GitHub
// associates a commit with both its original PR and a later promotion PR. If
// no preferred-base PR exists, the first GitHub association is retained for
// compatibility with the existing single-commit resolver.
func preferredAssociatedPull(pulls []associatedPull, baseRef string) *associatedPull {
	if baseRef != "" {
		for i := range pulls {
			if pulls[i].Base.Ref == baseRef {
				return &pulls[i]
			}
		}
	}
	if len(pulls) > 0 {
		return &pulls[0]
	}
	return nil
}

// selectSemverLabel enforces the invariant that a source pull request carries
// at most one semantic-version label.
func selectSemverLabel(labels []string, defaultLabel string) (string, error) {
	seen := make([]string, 0, 1)
	for _, label := range labels {
		if !slices.Contains(validSemverLabels, label) || slices.Contains(seen, label) {
			continue
		}
		seen = append(seen, label)
	}
	switch len(seen) {
	case 0:
		return defaultLabel, nil
	case 1:
		return seen[0], nil
	default:
		return "", fmt.Errorf("expected at most one semver label, found %s", strings.Join(seen, ", "))
	}
}

// bumpVersion increments one component of a stable semantic version and resets
// the lower-order components required by semantic-versioning rules.
func bumpVersion(currentVersion string, part string) (string, error) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(currentVersion))
	if match == nil {
		return "", fmt.Errorf("unsupported version format %q", currentVersion)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])

	switch part {
	case "major":
		major++
		minor = 0
		patch = 0
	case "minor":
		minor++
		patch = 0
	case "patch":
		patch++
	default:
		return "", fmt.Errorf("unsupported semver part %q", part)
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

// fatal writes a workflow-friendly error and exits unsuccessfully.
func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
