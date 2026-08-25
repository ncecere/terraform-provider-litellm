// Command release-notes extracts one exact, release-ready CHANGELOG section.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxChangelogBytes = 4 << 20
	maxSectionBytes   = 512 << 10
	maxTagBytes       = 128
)

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("release-notes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	tag := flags.String("tag", "", "exact release tag (for example v2.0.2)")
	changelog := flags.String("changelog", "CHANGELOG.md", "CHANGELOG path")
	output := flags.String("output", "-", "output path, or - for stdout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "release-notes: positional arguments are not accepted")
		return 2
	}

	version, err := versionFromTag(*tag)
	if err != nil {
		fmt.Fprintf(stderr, "release-notes: %v\n", err)
		return 1
	}
	body, err := readBounded(*changelog, maxChangelogBytes)
	if err != nil {
		fmt.Fprintln(stderr, "release-notes: unable to read the bounded changelog")
		return 1
	}
	notes, err := extractReleaseNotes(body, version)
	if err != nil {
		fmt.Fprintf(stderr, "release-notes: %v\n", err)
		return 1
	}

	if *output == "-" {
		_, err = stdout.Write(notes)
	} else {
		err = os.WriteFile(*output, notes, 0o600)
	}
	if err != nil {
		fmt.Fprintln(stderr, "release-notes: unable to write release notes")
		return 1
	}
	return 0
}

func versionFromTag(tag string) (string, error) {
	if tag == "" || len(tag) > maxTagBytes {
		return "", errors.New("tag must be a bounded exact semantic version")
	}
	version := strings.TrimPrefix(tag, "v")
	if !semverPattern.MatchString(version) {
		return "", errors.New("tag must be an exact semantic version, optionally prefixed by v")
	}
	return version, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.Size() > limit {
		return nil, errors.New("changelog exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) > limit || !utf8.Valid(body) {
		return nil, errors.New("changelog is not bounded UTF-8 text")
	}
	return body, nil
}

func extractReleaseNotes(changelog []byte, version string) ([]byte, error) {
	if !semverPattern.MatchString(version) {
		return nil, errors.New("requested version is not an exact semantic version")
	}

	exactTitle := regexp.MustCompile(`^\[` + regexp.QuoteMeta(version) + `\](?: - [0-9]{4}-[0-9]{2}-[0-9]{2})?$`)
	headings := markdownHeadings(changelog)
	var matches []markdownHeading
	for _, heading := range headings {
		if heading.level == 2 && exactTitle.MatchString(heading.title) {
			matches = append(matches, heading)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no changelog section exists for version %s", version)
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("changelog contains duplicate sections for version %s", version)
	}

	start := matches[0].contentStart
	end := len(changelog)
	for _, heading := range headings {
		if heading.start >= start && heading.level == 2 {
			end = heading.start
			break
		}
	}
	section := changelog[start:end]
	if len(section) > maxSectionBytes {
		return nil, fmt.Errorf("changelog section for version %s exceeds the size limit", version)
	}
	if len(strings.TrimSpace(string(section))) == 0 {
		return nil, fmt.Errorf("changelog section for version %s is empty", version)
	}

	sectionHeadings := markdownHeadings(section)
	for _, subsection := range []string{"Upgrade Notes", "Security"} {
		if err := requireSubsection(section, sectionHeadings, version, subsection); err != nil {
			return nil, err
		}
	}

	return []byte(strings.TrimSpace(string(section)) + "\n"), nil
}

func requireSubsection(section []byte, headings []markdownHeading, version, name string) error {
	var matches []markdownHeading
	for _, heading := range headings {
		if heading.level == 3 && heading.title == name {
			matches = append(matches, heading)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("changelog section for version %s is missing the %s subsection", version, name)
	}
	if len(matches) != 1 {
		return fmt.Errorf("changelog section for version %s contains duplicate %s subsections", version, name)
	}

	start := matches[0].contentStart
	end := len(section)
	for _, heading := range headings {
		if heading.start >= start && heading.level <= 3 {
			end = heading.start
			break
		}
	}
	if len(strings.TrimSpace(string(section[start:end]))) == 0 {
		return fmt.Errorf("changelog section for version %s has an empty %s subsection", version, name)
	}
	return nil
}

type markdownHeading struct {
	level        int
	title        string
	start        int
	contentStart int
}

// markdownHeadings recognizes ATX headings outside fenced code blocks. This is
// intentionally a bounded changelog parser, not a general Markdown renderer.
func markdownHeadings(body []byte) []markdownHeading {
	var headings []markdownHeading
	var fence byte
	var fenceWidth int

	for offset := 0; offset < len(body); {
		relativeEnd := bytes.IndexByte(body[offset:], '\n')
		lineEnd := len(body)
		contentStart := len(body)
		if relativeEnd >= 0 {
			lineEnd = offset + relativeEnd
			contentStart = lineEnd + 1
		}
		line := bytes.TrimSuffix(body[offset:lineEnd], []byte{'\r'})
		trimmed := trimMarkdownIndent(line)

		if marker, width, closing := markdownFence(trimmed, fence, fenceWidth); marker != 0 {
			if fence == 0 && !closing {
				fence, fenceWidth = marker, width
			} else if fence != 0 && closing {
				fence, fenceWidth = 0, 0
			}
			offset = contentStart
			continue
		}
		if fence == 0 {
			if level, title, ok := markdownATXHeading(trimmed); ok {
				headings = append(headings, markdownHeading{level: level, title: title, start: offset, contentStart: contentStart})
			}
		}
		offset = contentStart
	}
	return headings
}

func trimMarkdownIndent(line []byte) []byte {
	for count := 0; count < 3 && len(line) > 0 && line[0] == ' '; count++ {
		line = line[1:]
	}
	return line
}

func markdownFence(line []byte, active byte, activeWidth int) (marker byte, width int, closing bool) {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0, 0, false
	}
	marker = line[0]
	for width < len(line) && line[width] == marker {
		width++
	}
	if width < 3 {
		return 0, 0, false
	}
	if active == 0 {
		return marker, width, false
	}
	if marker == active && width >= activeWidth && len(bytes.TrimSpace(line[width:])) == 0 {
		return marker, width, true
	}
	return 0, 0, false
}

func markdownATXHeading(line []byte) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level == len(line) || (line[level] != ' ' && line[level] != '\t') {
		return 0, "", false
	}
	title := strings.TrimSpace(string(line[level:]))
	if title == "" {
		return 0, "", false
	}
	if end := strings.LastIndex(title, " #"); end >= 0 && strings.Trim(title[end+1:], "#") == "" {
		title = strings.TrimSpace(title[:end])
	}
	return level, title, true
}
