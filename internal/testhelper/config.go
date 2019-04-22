package testhelper

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

var cfgHeaderRegex = regexp.MustCompile(`^\[(.*?)\]$`)

// ConfigFile allows access to the different sections of a git config file
type ConfigFile map[string][]string

// ParseConfig will attempt to parse a config file into sections
func ParseConfig(src io.Reader) (ConfigFile, error) {
	scanner := bufio.NewScanner(src)

	currentSection := ""
	parsed := map[string][]string{}

	for scanner.Scan() {
		line := scanner.Text()

		matches := cfgHeaderRegex.FindStringSubmatch(line)
		if len(matches) == 2 {
			currentSection = matches[1]
			continue
		}

		parsed[currentSection] = append(
			parsed[currentSection],
			strings.TrimSpace(line),
		)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return parsed, nil
}

var configPairRegex = regexp.MustCompile(`^(.*?) = (.*?)$`)

// GetValue will return the value for a configuration line int the specified
// section for the provided key
func (cf ConfigFile) GetValue(section, key string) (string, bool) {
	pairs := cf[section]

	for _, pair := range pairs {
		matches := configPairRegex.FindStringSubmatch(pair)
		if len(matches) != 3 {
			continue
		}

		k, v := matches[1], matches[2]
		if k == key {
			return v, true
		}
	}

	return "", false
}
