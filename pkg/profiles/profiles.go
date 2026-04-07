// Package profiles reads available AWS profiles and filters them by regex.
package profiles

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"gopkg.in/ini.v1"
)

// DefaultFilter is the regex applied when no filter is specified.
const DefaultFilter = `ReadOnly|ViewOnly`

// Profile is a discovered AWS profile with its key metadata.
type Profile struct {
	Name      string `json:"name"`
	AccountID string `json:"account_id,omitempty"`
	Region    string `json:"region,omitempty"`
}

// List returns AWS profiles matching filter as structured Profile values. If
// filter is empty, DefaultFilter is used. Returns an error if filter is not a
// valid regex. Results are sorted alphabetically by name.
//
// Profile discovery scans the config and credentials files for section
// headers, then validates each candidate with config.LoadSharedConfigProfile
// so that all profile-format details (the [profile name] prefix, deduplication,
// env-var file overrides) are handled by the SDK.
func List(ctx context.Context, filter string) ([]Profile, error) {
	if filter == "" {
		filter = DefaultFilter
	}
	re, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("profiles: invalid filter regex %q: %w", filter, err)
	}

	candidates, grantedIDs, configFiles, credFiles, err := candidateProfileNames()
	if err != nil {
		return nil, err
	}

	// loadOpts pins the SDK to the same files we scanned so env-var overrides
	// are honoured consistently.
	loadOpts := func(o *awsconfig.LoadSharedConfigOptions) {
		o.ConfigFiles = configFiles
		o.CredentialsFiles = credFiles
	}

	var result []Profile
	for _, name := range candidates {
		if sharedCfg, err := awsconfig.LoadSharedConfigProfile(ctx, name, loadOpts); err == nil {
			if re.MatchString(name) {
				result = append(result, Profile{
					Name:      name,
					AccountID: resolveAccountID(sharedCfg.RoleARN, grantedIDs[name]),
					Region:    sharedCfg.Region,
				})
			}
		}
		// SharedConfigProfileNotExistError (or any other error) → skip
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// resolveAccountID returns the AWS account ID from roleARN if present,
// otherwise falls back to grantedID (the granted_sso_account_id config key).
func resolveAccountID(roleARN, grantedID string) string {
	if roleARN != "" {
		return accountIDFromARN(roleARN)
	}
	return grantedID
}

// accountIDFromARN extracts the account ID from an AWS ARN.
// ARN format: arn:partition:service:region:account-id:resource
func accountIDFromARN(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) >= 5 {
		return parts[4]
	}
	return ""
}

// candidateProfileNames extracts candidate profile names from the AWS config
// and credentials files. It also returns a map of profile name →
// granted_sso_account_id for profiles where that key is present in the config
// file. Both are captured in a single pass over each file.
//
// The resolved file paths are returned so callers can forward them to
// LoadSharedConfigProfile for consistency with env-var overrides.
func candidateProfileNames() (candidates []string, grantedIDs map[string]string, configFiles []string, credFiles []string, err error) {
	configFiles = []string{configFilePath()}
	credFiles = []string{credentialsFilePath()}
	grantedIDs = map[string]string{}

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

	// Config file: strip the "profile " prefix the format requires.
	// Capture granted_sso_account_id in the same pass.
	for _, path := range configFiles {
		f, loadErr := ini.Load(path)
		if loadErr != nil {
			continue // missing file is not an error
		}
		for _, sec := range f.Sections() {
			raw := strings.TrimSpace(strings.TrimPrefix(sec.Name(), "profile "))
			add(raw)
			if key, keyErr := sec.GetKey("granted_sso_account_id"); keyErr == nil {
				grantedIDs[raw] = strings.TrimSpace(key.Value())
			}
		}
	}

	// Credentials file: section names are profile names directly.
	for _, path := range credFiles {
		f, loadErr := ini.Load(path)
		if loadErr != nil {
			continue
		}
		for _, sec := range f.Sections() {
			add(strings.TrimSpace(sec.Name()))
		}
	}

	return candidates, grantedIDs, configFiles, credFiles, nil
}

func configFilePath() string {
	return envOrFile("AWS_CONFIG_FILE", awsconfig.DefaultSharedConfigFilename())
}

func credentialsFilePath() string {
	return envOrFile("AWS_SHARED_CREDENTIALS_FILE", awsconfig.DefaultSharedCredentialsFilename())
}

func envOrFile(envKey, defaultPath string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultPath
}
