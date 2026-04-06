// Package profiles reads available AWS profiles and filters them by regex.
package profiles

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// DefaultFilter is the regex applied when no filter is specified.
const DefaultFilter = `ReadOnly|ViewOnly`

// List returns AWS profile names that match filter. If filter is empty,
// DefaultFilter is used. Returns an error if filter is not a valid regex.
// Results are sorted alphabetically.
func List(filter string) ([]string, error) {
	if filter == "" {
		filter = DefaultFilter
	}
	re, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("profiles: invalid filter regex %q: %w", filter, err)
	}

	names, err := readAllProfiles()
	if err != nil {
		return nil, err
	}

	var result []string
	for _, name := range names {
		if re.MatchString(name) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result, nil
}

// readAllProfiles collects unique profile names from both the AWS config file
// and the credentials file.
func readAllProfiles() ([]string, error) {
	seen := make(map[string]struct{})
	var names []string

	add := func(name string) {
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}

	if err := parseConfigFile(configFilePath(), add); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("profiles: read config file: %w", err)
	}

	if err := parseCredentialsFile(credentialsFilePath(), add); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("profiles: read credentials file: %w", err)
	}

	return names, nil
}

func configFilePath() string {
	if v := os.Getenv("AWS_CONFIG_FILE"); v != "" {
		return v
	}
	return awsconfig.DefaultSharedConfigFilename()
}

func credentialsFilePath() string {
	if v := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); v != "" {
		return v
	}
	return awsconfig.DefaultSharedCredentialsFilename()
}

// parseConfigFile reads [default] and [profile name] sections from an AWS
// config file, calling add for each profile name discovered.
func parseConfigFile(path string, add func(string)) error {
	return scanSections(path, func(section string) {
		if name, ok := configSectionName(section); ok {
			add(name)
		}
	})
}

// configSectionName parses an INI section header from an AWS config file.
// [default] → ("default", true); [profile foo] → ("foo", true); others → ("", false).
func configSectionName(section string) (string, bool) {
	inner := strings.TrimSpace(section[1 : len(section)-1])
	if inner == "default" {
		return "default", true
	}
	if rest, ok := strings.CutPrefix(inner, "profile "); ok {
		name := strings.TrimSpace(rest)
		if name != "" {
			return name, true
		}
	}
	return "", false
}

// parseCredentialsFile reads [name] sections from an AWS credentials file.
func parseCredentialsFile(path string, add func(string)) error {
	return scanSections(path, func(section string) {
		name := strings.TrimSpace(section[1 : len(section)-1])
		if name != "" {
			add(name)
		}
	})
}

// scanSections opens path and calls fn for each INI section header line
// (lines of the form "[...]"). It is the caller's responsibility to parse
// the section name from the raw line.
func scanSections(path string, fn func(line string)) error {
	f, err := os.Open(path) //nolint:gosec // path is from user AWS config env var or home dir, not external input
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			fn(line)
		}
	}
	return scanner.Err()
}
