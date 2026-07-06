package agy

import (
	"net/url"
	"regexp"
	"strings"
)

var markdownFileURI = regexp.MustCompile(`\]\(file://([^)]+)\)`)

func ParseModels(output string) []Model {
	lines := strings.Split(output, "\n")
	models := make([]Model, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		models = append(models, Model{Name: name})
	}
	return models
}

func PlanPath(output string) string {
	match := markdownFileURI.FindStringSubmatch(output)
	if len(match) != 2 {
		return ""
	}
	path, err := url.PathUnescape(match[1])
	if err != nil {
		return match[1]
	}
	return path
}

func PlanEntries(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789.[] xX"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
