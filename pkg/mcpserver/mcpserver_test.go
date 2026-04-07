package mcpserver_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/mcpserver"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// ---- test doubles ----

type fakeBroker struct {
	createResult broker.CreateResult
	createErr    error
	stopErr      error
	stopCalled   string
}

func (f *fakeBroker) CreateServer(_ context.Context, _, _ string) (broker.CreateResult, error) {
	return f.createResult, f.createErr
}

func (f *fakeBroker) StopServer(_ context.Context, url string) error {
	f.stopCalled = url
	return f.stopErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// newTestClient creates, starts, and initialises an InProcessClient for the
// given MCP server. The client is closed on test cleanup.
func newTestClient(t *testing.T, s *mcpserver.MCPServer) *client.Client {
	t.Helper()
	c, err := client.NewInProcessClient(s.Server())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	require.NoError(t, c.Start(context.Background()))

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	_, err = c.Initialize(context.Background(), initReq)
	require.NoError(t, err)

	return c
}

// callTool calls a named tool and returns the result.
func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	result, err := c.CallTool(context.Background(), req)
	require.NoError(t, err)
	return result
}

// firstText extracts the text from the first content item of a CallToolResult.
func firstText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content, "expected non-empty content")
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])
	return tc.Text
}

// ---- tests ----

// Test 1 (tracer bullet): list_profiles returns a JSON array of profile objects.
func TestListProfiles_ReturnsMatchingProfiles(t *testing.T) {
	lister := func(_ context.Context, _ string) ([]profiles.Profile, error) {
		return []profiles.Profile{
			{Name: "dev-ReadOnly", AccountID: "111122223333", Region: "us-east-1"},
			{Name: "prod-ReadOnly", AccountID: "444455556666", Region: "ap-southeast-2"},
		}, nil
	}
	s := mcpserver.New(mcpserver.Options{
		Broker:        &fakeBroker{},
		ListProfiles:  lister,
		ProfileFilter: "ReadOnly",
		Logger:        discardLogger(),
	})
	c := newTestClient(t, s)

	result := callTool(t, c, "list_profiles", nil)

	require.False(t, result.IsError)
	assert.JSONEq(t, `[
		{"name":"dev-ReadOnly","account_id":"111122223333","region":"us-east-1"},
		{"name":"prod-ReadOnly","account_id":"444455556666","region":"ap-southeast-2"}
	]`, firstText(t, result))
}

// Test 2: list_profiles with empty result returns empty JSON array.
func TestListProfiles_EmptyResult_ReturnsEmptyArray(t *testing.T) {
	lister := func(_ context.Context, _ string) ([]profiles.Profile, error) {
		return nil, nil
	}
	s := mcpserver.New(mcpserver.Options{
		Broker:       &fakeBroker{},
		ListProfiles: lister,
		Logger:       discardLogger(),
	})
	c := newTestClient(t, s)

	result := callTool(t, c, "list_profiles", nil)

	require.False(t, result.IsError)
	text := firstText(t, result)
	var profs []profiles.Profile
	require.NoError(t, json.Unmarshal([]byte(text), &profs))
	assert.Empty(t, profs)
}

// Test 3: create_server returns local URL and port.
func TestCreateServer_ReturnsLocalURLAndPort(t *testing.T) {
	b := &fakeBroker{
		createResult: broker.CreateResult{LocalURL: "http://127.0.0.1:12345"},
	}
	s := mcpserver.New(mcpserver.Options{
		Broker:       b,
		ListProfiles: func(_ context.Context, _ string) ([]profiles.Profile, error) { return nil, nil },
		Logger:       discardLogger(),
	})
	c := newTestClient(t, s)

	result := callTool(t, c, "create_server", map[string]any{
		"profile": "prod",
		"region":  "us-east-1",
	})

	require.False(t, result.IsError)
	text := firstText(t, result)
	assert.Contains(t, text, "http://127.0.0.1:12345")
	assert.Contains(t, text, `"port":"12345"`)
	assert.NotContains(t, text, "docker")
}

// Test 4: stop_server returns success for a known URL.
func TestStopServer_KnownURL_ReturnsSuccess(t *testing.T) {
	b := &fakeBroker{}
	s := mcpserver.New(mcpserver.Options{
		Broker:       b,
		ListProfiles: func(_ context.Context, _ string) ([]profiles.Profile, error) { return nil, nil },
		Logger:       discardLogger(),
	})
	c := newTestClient(t, s)

	result := callTool(t, c, "stop_server", map[string]any{"url": "http://127.0.0.1:12345"})

	require.False(t, result.IsError)
	assert.Equal(t, "http://127.0.0.1:12345", b.stopCalled)
}

// Test 6: stop_server returns an error result for an unknown URL.
func TestStopServer_UnknownURL_ReturnsErrorResult(t *testing.T) {
	b := &fakeBroker{stopErr: assert.AnError}
	s := mcpserver.New(mcpserver.Options{
		Broker:       b,
		ListProfiles: func(_ context.Context, _ string) ([]profiles.Profile, error) { return nil, nil },
		Logger:       discardLogger(),
	})
	c := newTestClient(t, s)

	result := callTool(t, c, "stop_server", map[string]any{"url": "http://127.0.0.1:99999"})

	assert.True(t, result.IsError)
}
