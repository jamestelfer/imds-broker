// Package mcpserver is a thin adapter that exposes broker and profiles
// functionality as MCP tools over stdio.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/url"
	"regexp"

	"github.com/mark3labs/mcp-go/mcp"
	mcplib "github.com/mark3labs/mcp-go/server"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// ProfileFilter determines whether a named AWS profile is permitted.
type ProfileFilter interface {
	Allowed(name string) bool
}

type regexFilter struct{ re *regexp.Regexp }

func (f *regexFilter) Allowed(name string) bool { return f.re.MatchString(name) }

// NewProfileFilter returns a ProfileFilter backed by a compiled regex.
// Uses profiles.DefaultFilter when filter is empty.
// Returns an error if filter is not a valid regular expression.
func NewProfileFilter(filter string) (ProfileFilter, error) {
	if filter == "" {
		filter = profiles.DefaultFilter
	}
	re, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid profile filter %q: %w", filter, err)
	}
	return &regexFilter{re: re}, nil
}

// ProfileLister returns all discoverable AWS profiles. Filtering is handled
// separately by ProfileFilter.
type ProfileLister func(ctx context.Context) ([]profiles.Profile, error)

// BrokerFace is the broker subset required by the MCP server.
type BrokerFace interface {
	CreateServer(ctx context.Context, profile, region string) (broker.CreateResult, error)
	StopServer(ctx context.Context, serverURL string) error
}

// Options configures the MCP server.
type Options struct {
	Broker       BrokerFace
	ListProfiles ProfileLister
	Filter       ProfileFilter
	Logger       *slog.Logger
}

// MCPServer wraps the underlying MCP server so callers can access it via
// Server() for transport setup (e.g. ServeStdio) while tests can create an
// InProcessClient.
type MCPServer struct {
	s *mcplib.MCPServer
}

// Server returns the underlying mark3labs/mcp-go server.
func (m *MCPServer) Server() *mcplib.MCPServer {
	return m.s
}

// ServeStdio serves the MCP server over stdio, blocking until stdin EOF or
// SIGINT/SIGTERM. Returns any transport error.
func (m *MCPServer) ServeStdio() error {
	return mcplib.ServeStdio(m.s)
}

// New creates an MCP server with list_profiles, create_server, and stop_server
// tools. No business logic lives here — all delegation goes to Broker and
// ListProfiles.
func New(opts Options) *MCPServer {
	s := mcplib.NewMCPServer("imds-broker", "1.0.0",
		mcplib.WithToolCapabilities(false),
	)

	s.AddTool(listProfilesTool(), listProfilesHandler(opts.ListProfiles, opts.Filter, opts.Logger))
	s.AddTool(createServerTool(), createServerHandler(opts.Broker, opts.Filter, opts.Logger))
	s.AddTool(stopServerTool(), stopServerHandler(opts.Broker, opts.Logger))

	return &MCPServer{s: s}
}

// listProfilesTool defines the list_profiles MCP tool.
func listProfilesTool() mcp.Tool {
	return mcp.NewTool("list_profiles",
		mcp.WithDescription("List AWS profiles matching the configured filter"),
	)
}

// requestID returns a short hex string for correlating log lines within a
// single tool invocation.
func requestID() string {
	return fmt.Sprintf("%x", rand.Uint64()) //nolint:gosec
}

// listProfilesHandler returns a handler that calls the profile lister and
// returns filtered results as a JSON array.
func listProfilesHandler(lister ProfileLister, filter ProfileFilter, logger *slog.Logger) mcplib.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("mcp tool call", "tool", "list_profiles", "request_id", requestID())
		all, err := lister(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		names := []profiles.Profile{}
		for _, p := range all {
			if filter.Allowed(p.Name) {
				names = append(names, p)
			}
		}
		b, err := json.Marshal(names)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

// createServerTool defines the create_server MCP tool.
func createServerTool() mcp.Tool {
	return mcp.NewTool("create_server",
		mcp.WithDescription("Start (or return the existing) IMDS server for an AWS profile"),
		mcp.WithString("profile",
			mcp.Required(),
			mcp.Description("AWS profile name"),
		),
		mcp.WithString("region",
			mcp.Description("AWS region (defaults to profile-configured region)"),
		),
	)
}

// serverURLs is the JSON shape returned by create_server.
type serverURLs struct {
	LocalURL string `json:"local_url"`
	Port     string `json:"port"`
}

// createServerHandler returns a handler that starts (or returns) an IMDS server.
func createServerHandler(b BrokerFace, filter ProfileFilter, logger *slog.Logger) mcplib.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("mcp tool call", "tool", "create_server", "request_id", requestID())
		profile, err := req.RequireString("profile")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if !filter.Allowed(profile) {
			return mcp.NewToolResultError(fmt.Sprintf("profile %q does not match the configured filter", profile)), nil
		}
		region := req.GetString("region", "")

		result, err := b.CreateServer(ctx, profile, region)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		urls := serverURLs{LocalURL: result.LocalURL, Port: portFromURL(result.LocalURL)}
		out, err := json.Marshal(urls)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

// stopServerTool defines the stop_server MCP tool.
func stopServerTool() mcp.Tool {
	return mcp.NewTool("stop_server",
		mcp.WithDescription("Stop a running IMDS server"),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("The local URL of the server to stop"),
		),
	)
}

// stopServerHandler returns a handler that stops a running IMDS server.
func stopServerHandler(b BrokerFace, logger *slog.Logger) mcplib.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("mcp tool call", "tool", "stop_server", "request_id", requestID())
		url, err := req.RequireString("url")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if err := b.StopServer(ctx, url); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("stopped"), nil
	}
}

// portFromURL extracts the port from a URL string (e.g. "http://127.0.0.1:8080" → "8080").
// Returns an empty string if the URL is malformed or has no explicit port.
func portFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return ""
	}
	return port
}
