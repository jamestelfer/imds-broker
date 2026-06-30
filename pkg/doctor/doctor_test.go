package doctor_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/doctor"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

func discovered(names ...string) []profiles.Profile {
	out := make([]profiles.Profile, 0, len(names))
	for _, n := range names {
		out = append(out, profiles.Profile{Name: n})
	}
	return out
}

// Tracer bullet: a config-file filter is resolved with the correct source and
// matched count.
func TestBuild_ConfigFilterResolvesAndCounts(t *testing.T) {
	in := doctor.Inputs{ConfigFilter: "ViewOnly"}
	rep, err := doctor.Build(in, discovered("dev-ViewOnly", "prod-ReadOnly", "admin"), nil)
	require.NoError(t, err)
	assert.Equal(t, "ViewOnly", rep.Filter)
	assert.Equal(t, "config file", rep.FilterSource)
	assert.Equal(t, 3, rep.TotalProfiles)
	assert.Equal(t, 1, rep.MatchedProfiles)
}

// An explicitly empty --profile-filter must report the built-in default it
// actually uses for matching, not the empty string, while keeping the flag
// source label.
func TestBuild_EmptyFlagNormalisesToDefault(t *testing.T) {
	in := doctor.Inputs{FilterSet: true, FilterValue: ""}
	rep, err := doctor.Build(in, discovered("dev-ViewOnly", "prod-ReadOnly", "admin"), nil)
	require.NoError(t, err)
	assert.Equal(t, profiles.DefaultFilter, rep.Filter)
	assert.Equal(t, "command-line flag", rep.FilterSource)
	// ReadOnly|ViewOnly matches two of the three profiles.
	assert.Equal(t, 2, rep.MatchedProfiles)
}

func TestBuild_RegionSource(t *testing.T) {
	rep, err := doctor.Build(doctor.Inputs{ConfigRegion: "ap-southeast-2"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "ap-southeast-2", rep.Region)
	assert.Equal(t, "config file", rep.RegionSource)

	rep, err = doctor.Build(doctor.Inputs{}, nil, nil)
	require.NoError(t, err)
	assert.Contains(t, rep.Region, "profile-configured")
	assert.Equal(t, "built-in default", rep.RegionSource)
}

func TestBuild_LogLevelSource(t *testing.T) {
	// flag wins over config
	rep, err := doctor.Build(doctor.Inputs{LogLevelSet: true, LogLevelValue: "warn", ConfigLogLevel: "debug"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "warn", rep.LogLevel)
	assert.Equal(t, "command-line flag", rep.LogLevelSource)

	// config when flag absent
	rep, err = doctor.Build(doctor.Inputs{ConfigLogLevel: "debug"}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "debug", rep.LogLevel)
	assert.Equal(t, "config file", rep.LogLevelSource)

	// built-in default otherwise
	rep, err = doctor.Build(doctor.Inputs{}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "info", rep.LogLevel)
	assert.Equal(t, "built-in default", rep.LogLevelSource)
}

func TestBuild_FilterSourceEnvAndDefault(t *testing.T) {
	rep, err := doctor.Build(doctor.Inputs{FilterSet: true, FilterValue: "Admin", FilterFromEnv: true}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "Admin", rep.Filter)
	assert.Contains(t, rep.FilterSource, "environment")

	rep, err = doctor.Build(doctor.Inputs{}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, profiles.DefaultFilter, rep.Filter)
	assert.Equal(t, "built-in default", rep.FilterSource)
}

func TestBuild_InvalidFilterReturnsConfigError(t *testing.T) {
	_, err := doctor.Build(doctor.Inputs{FilterSet: true, FilterValue: "[invalid"}, discovered("x"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "profile filter")
}

func TestBuild_DiscoveryErrorRecordedNoConfigError(t *testing.T) {
	discErr := errors.New("cannot read aws config")
	rep, err := doctor.Build(doctor.Inputs{ConfigFilter: "ViewOnly"}, nil, discErr)
	require.NoError(t, err)
	assert.Equal(t, discErr, rep.DiscoveryErr)
	assert.Equal(t, 0, rep.TotalProfiles)
	assert.Equal(t, 0, rep.MatchedProfiles)
}

func TestRender_IncludesSectionsAndWarning(t *testing.T) {
	rep, err := doctor.Build(doctor.Inputs{
		ConfigPath:   "/cfg/config.yaml",
		ConfigFound:  true,
		ConfigFilter: "ViewOnly",
		ConfigRegion: "ap-southeast-2",
	}, discovered("dev-ViewOnly", "admin"), nil)
	require.NoError(t, err)

	out := doctor.Render(rep)
	assert.Contains(t, out, "/cfg/config.yaml")
	assert.Contains(t, out, "found")
	assert.Contains(t, out, "profile filter: ViewOnly  (source: config file)")
	assert.Contains(t, out, "region:         ap-southeast-2  (source: config file)")
	assert.Contains(t, out, "discoverable profiles: 2")
	assert.Contains(t, out, "matched by filter:     1")
	assert.Contains(t, out, "imds-broker profiles")
	assert.Contains(t, out, "advisory only")
	// must never leak profile names
	assert.NotContains(t, out, "dev-ViewOnly")
	assert.NotContains(t, out, "admin")
}

func TestRender_DiscoveryFailureDistinct(t *testing.T) {
	rep, err := doctor.Build(doctor.Inputs{}, nil, errors.New("boom"))
	require.NoError(t, err)
	out := doctor.Render(rep)
	assert.Contains(t, out, "failed to discover local profiles")
	assert.Contains(t, out, "not a configuration error")
}
