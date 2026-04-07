// Package mcpserver is a thin adapter that exposes broker and profiles
// functionality as MCP tools over stdio.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"

	"github.com/mark3labs/mcp-go/mcp"
	mcplib "github.com/mark3labs/mcp-go/server"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/profiles"
)

// ProfileLister is the signature of profiles.List.
type ProfileLister func(ctx context.Context, filter string) ([]profiles.Profile, error)

// BrokerFace is the broker subset required by the MCP server.
type BrokerFace interface {
	CreateServer(ctx context.Context, profile, region string) (broker.CreateResult, error)
	StopServer(ctx context.Context, serverURL string) error
}

// Options configures the MCP server.
type Options struct {
	Broker        BrokerFace
	ListProfiles  ProfileLister
	ProfileFilter string
	Logger        *slog.Logger
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

	s.AddTool(listProfilesTool(), listProfilesHandler(opts.ListProfiles, opts.ProfileFilter, opts.Logger))
	s.AddTool(createServerTool(), createServerHandler(opts.Broker, opts.Logger))
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
// returns the result as a JSON array.
func listProfilesHandler(lister ProfileLister, filter string, logger *slog.Logger) mcplib.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("mcp tool call", "tool", "list_profiles", "request_id", requestID())
		names, err := lister(ctx, filter)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if names == nil {
			names = []profiles.Profile{}
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
	LocalURL  string `json:"local_url"`
	DockerURL string `json:"docker_url,omitempty"`
}

// createServerHandler returns a handler that starts (or returns) an IMDS server.
func createServerHandler(b BrokerFace, logger *slog.Logger) mcplib.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("mcp tool call", "tool", "create_server", "request_id", requestID())
		profile, err := req.RequireString("profile")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		region := req.GetString("region", "")

		result, err := b.CreateServer(ctx, profile, region)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		urls := serverURLs{LocalURL: result.LocalURL, DockerURL: result.DockerURL}
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
			mcp.Description("The local or Docker URL of the server to stop"),
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
