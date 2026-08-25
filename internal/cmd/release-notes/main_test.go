package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validChangelog = `# Changelog

## [Unreleased]

### Upgrade Notes
- Not released.

### Security
- Not released.

## [1.2.3] - 2026-01-02

### Added
- Exact release content.

### Upgrade Notes
- Review the provider source before upgrading.

### Security
- No new security behavior.

## [1.2.2] - 2025-12-01

### Upgrade Notes
- Older notes.

### Security
- Older security notes.
`

func TestVersionFromTag(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"1.2.3", "v1.2.3", "v1.2.3-rc.1+build.5"} {
		tag := tag
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			if _, err := versionFromTag(tag); err != nil {
				t.Fatalf("versionFromTag(%q): %v", tag, err)
			}
		})
	}
	for _, tag := range []string{"", "Unreleased", "v1.2", "v1.2.x", "v01.2.3", "v1.2.3 anything"} {
		tag := tag
		t.Run("reject_"+tag, func(t *testing.T) {
			t.Parallel()
			if _, err := versionFromTag(tag); err == nil {
				t.Fatalf("versionFromTag(%q) unexpectedly succeeded", tag)
			}
		})
	}
}

func TestExtractReleaseNotesExactSection(t *testing.T) {
	t.Parallel()

	notes, err := extractReleaseNotes([]byte(validChangelog), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	text := string(notes)
	if !strings.Contains(text, "Exact release content") {
		t.Fatalf("notes did not contain selected release content: %q", text)
	}
	if strings.Contains(text, "Not released") || strings.Contains(text, "Older notes") || strings.Contains(text, "## [1.2.3]") {
		t.Fatalf("notes escaped the exact section body: %q", text)
	}
}

func TestExtractReleaseNotesIgnoresHeadingsInsideFences(t *testing.T) {
	t.Parallel()

	changelog := "# Changelog\n\n```markdown\n## [1.2.3]\n### Upgrade Notes\n### Security\n```\n\n" + validChangelog
	notes, err := extractReleaseNotes([]byte(changelog), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notes), "Exact release content") {
		t.Fatalf("unexpected notes: %q", notes)
	}
}

func TestExtractPrereleaseBuildNotesExactOutputAndFencedHeadings(t *testing.T) {
	t.Parallel()

	const version = "2.1.0-rc.1+build.5"
	const expected = `### Upgrade Notes
- Upgrade this release candidate deliberately.

` + "```markdown\n## [2.1.0-rc.1+build.5]\n### Security\n- Fenced text is not a subsection.\n```\n\n" + `### Security
- No security behavior changed.
`
	changelog := `# Changelog

` + "```markdown\n## [2.1.0-rc.1+build.5]\n### Upgrade Notes\n- Fake section.\n### Security\n- Fake section.\n```\n\n" + `## [2.1.0-rc.1+build.5] - 2026-08-25

` + expected + `
## [2.0.1] - 2026-06-12

### Upgrade Notes
- Older upgrade notes.

### Security
- Older security notes.
`

	parsedVersion, err := versionFromTag("v" + version)
	if err != nil {
		t.Fatal(err)
	}
	if parsedVersion != version {
		t.Fatalf("versionFromTag returned %q, want %q", parsedVersion, version)
	}
	notes, err := extractReleaseNotes([]byte(changelog), parsedVersion)
	if err != nil {
		t.Fatal(err)
	}
	if string(notes) != expected {
		t.Fatalf("exact prerelease notes mismatch:\nactual:\n%q\nexpected:\n%q", notes, expected)
	}
}

func TestExtractPrereleaseBuildNotesRejectsDuplicateExactSection(t *testing.T) {
	t.Parallel()

	const section = `## [2.1.0-rc.1+build.5]
### Upgrade Notes
- Upgrade.
### Security
- Security.
`
	changelog := "# Changelog\n\n" + section + "\n" + section
	if _, err := extractReleaseNotes([]byte(changelog), "2.1.0-rc.1+build.5"); err == nil || !strings.Contains(err.Error(), "duplicate sections") {
		t.Fatalf("duplicate exact prerelease section error = %v", err)
	}
}

func TestExtractPrereleaseBuildNotesRejectsMissingOrEmptyRequiredSubsections(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"missing Upgrade Notes": `### Security
- Security.
`,
		"empty Upgrade Notes": `### Upgrade Notes
### Security
- Security.
`,
		"missing Security": `### Upgrade Notes
- Upgrade.
`,
		"empty Security": `### Upgrade Notes
- Upgrade.
### Security
## [2.0.1]
`,
	}
	for name, body := range tests {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changelog := "# Changelog\n\n## [2.1.0-rc.1+build.5]\n" + body
			if _, err := extractReleaseNotes([]byte(changelog), "2.1.0-rc.1+build.5"); err == nil {
				t.Fatal("extractReleaseNotes unexpectedly succeeded")
			}
		})
	}
}

func TestExtractReleaseNotesRejectsInvalidSections(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"missing section": `# Changelog
## [1.2.2]
### Upgrade Notes
- Upgrade.
### Security
- Security.
`,
		"malformed section heading": `# Changelog
## [1.2.3] someday
### Upgrade Notes
- Upgrade.
### Security
- Security.
`,
		"duplicate section": validChangelog + `
## [1.2.3]
### Upgrade Notes
- Duplicate.
### Security
- Duplicate.
`,
		"empty section": `# Changelog
## [1.2.3]
## [1.2.2]
`,
		"missing upgrade notes": `# Changelog
## [1.2.3]
### Security
- Security.
`,
		"empty upgrade notes": `# Changelog
## [1.2.3]
### Upgrade Notes
### Security
- Security.
`,
		"duplicate upgrade notes": `# Changelog
## [1.2.3]
### Upgrade Notes
- First.
### Upgrade Notes
- Second.
### Security
- Security.
`,
		"missing security": `# Changelog
## [1.2.3]
### Upgrade Notes
- Upgrade.
`,
		"empty security": `# Changelog
## [1.2.3]
### Upgrade Notes
- Upgrade.
### Security
## [1.2.2]
`,
		"duplicate security": `# Changelog
## [1.2.3]
### Upgrade Notes
- Upgrade.
### Security
- First.
### Security
- Second.
`,
	}

	for name, changelog := range tests {
		name, changelog := name, changelog
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := extractReleaseNotes([]byte(changelog), "1.2.3"); err == nil {
				t.Fatal("extractReleaseNotes unexpectedly succeeded")
			}
		})
	}
}

func TestRunWritesOnlySelectedNotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	changelogPath := filepath.Join(dir, "CHANGELOG.md")
	outputPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(changelogPath, []byte(validChangelog), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-tag", "v1.2.3", "-changelog", changelogPath, "-output", outputPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	notes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notes), "Exact release content") || strings.Contains(string(notes), "Not released") {
		t.Fatalf("unexpected generated notes: %q", notes)
	}
}

func TestReadBoundedRejectsOversizedChangelog(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, 33), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBounded(path, 32); err == nil {
		t.Fatal("readBounded unexpectedly accepted oversized input")
	}
}

func TestRunDiagnosticsDoNotDumpChangelogBody(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	changelogPath := filepath.Join(dir, "CHANGELOG.md")
	secretMarker := "ARBITRARY_CHANGELOG_BODY_MUST_NOT_APPEAR"
	body := "## [1.2.3]\n" + secretMarker + "\n"
	if err := os.WriteFile(changelogPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-tag", "v1.2.3", "-changelog", changelogPath}, &stdout, &stderr); code == 0 {
		t.Fatal("run unexpectedly succeeded")
	}
	if strings.Contains(stderr.String(), secretMarker) {
		t.Fatalf("diagnostic exposed changelog content: %q", stderr.String())
	}
}
