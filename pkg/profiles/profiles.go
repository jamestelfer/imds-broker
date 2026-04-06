// Package profiles reads available AWS profiles and filters them by regex.
package profiles

import (
	"bufio"
	"context"
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
//
// Profile discovery scans the config and credentials files for section
// headers, then validates each candidate with config.LoadSharedConfigProfile
// so that all profile-format details (the [profile name] prefix, deduplication,
// env-var file overrides) are handled by the SDK.
func List(ctx context.Context, filter string) ([]string, error) {
	if filter == "" {
		filter = DefaultFilter
	}
	re, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("profiles: invalid filter regex %q: %w", filter, err)
	}

	candidates, configFiles, credFiles, err := candidateProfileNames()
	if err != nil {
		return nil, err
	}

	// loadOpts pins the SDK to the same files we scanned so env-var overrides
	// are honoured consistently.
	loadOpts := func(o *awsconfig.LoadSharedConfigOptions) {
		o.ConfigFiles = configFiles
		o.CredentialsFiles = credFiles
	}

	var result []string
	for _, name := range candidates {
		if _, err := awsconfig.LoadSharedConfigProfile(ctx, name, loadOpts); err == nil {
			if re.MatchString(name) {
				result = append(result, name)
			}
		}
		// SharedConfigProfileNotExistError (or any other error) → skip
	}
	sort.Strings(result)
	return result, nil
}

// candidateProfileNames extracts candidate profile names from the AWS config
// and credentials files and returns them along with the resolved file paths
// (which callers should forward to LoadSharedConfigProfile for consistency).
//
// For the config file, the "profile " prefix required by the config-file format
// is stripped so that the candidate matches the name expected by the SDK. The
// SDK itself validates which candidates are real profiles; we only extract names.
func candidateProfileNames() (candidates []string, configFiles []string, credFiles []string, err error) {
	configFiles = []string{configFilePath()}
	credFiles = []string{credentialsFilePath()}

	seen := make(map[string]struct{})
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			candidates = append(candidates, name)
		}
	}

	// Config file: strip the "profile " prefix the format requires; the SDK
	// validates the result. Sections without that prefix (like [default],
	// [sso-session …]) are passed through unchanged and will be filtered out
	// by LoadSharedConfigProfile if they are not real profiles.
	for _, path := range configFiles {
		if err = scanSections(path, func(section string) {
			inner := strings.TrimSpace(section[1 : len(section)-1])
			name, _ := strings.CutPrefix(inner, "profile ")
			add(strings.TrimSpace(name))
		}); err != nil && !os.IsNotExist(err) {
			return nil, nil, nil, fmt.Errorf("profiles: read config file: %w", err)
		}
	}

	// Credentials file: section names are profile names directly.
	for _, path := range credFiles {
		if err = scanSections(path, func(section string) {
			add(strings.TrimSpace(section[1 : len(section)-1]))
		}); err != nil && !os.IsNotExist(err) {
			return nil, nil, nil, fmt.Errorf("profiles: read credentials file: %w", err)
		}
	}

	return candidates, configFiles, credFiles, nil
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

// scanSections opens path and calls fn for each INI section header line
// (lines matching "[…]"). It is the caller's responsibility to interpret
// the section name.
func scanSections(path string, fn func(line string)) (err error) {
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
