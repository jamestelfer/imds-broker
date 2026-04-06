package broker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
)

// Server is the interface the broker uses to interact with IMDS server instances.
type Server interface {
	URLs() []string
	Stop()
	Done() <-chan struct{}
}

// ServerFactory creates a new IMDS server for the given profile, region, and
// bind addresses. Returns an error if the profile is invalid or the server
// cannot start.
type ServerFactory func(ctx context.Context, profile, region string, bindAddrs []string, logger *slog.Logger) (Server, error)

// CreateResult contains the URLs for a created (or existing) IMDS server.
type CreateResult struct {
	LocalURL  string
	DockerURL string // empty if Docker gateway was not discovered
}

// Options configures a Broker.
type Options struct {
	Logger        *slog.Logger
	Executor      CommandExecutor
	ServerFactory ServerFactory
}

// entry tracks a running IMDS server instance and its registered URLs.
type entry struct {
	server    Server
	localURL  string
	dockerURL string // empty when no Docker gateway
	key       string
	stopped   bool // true when the broker itself called Stop
}

// isCrashed reports whether the server's done channel has closed without the
// broker having requested a stop.
func (e *entry) isCrashed() bool {
	select {
	case <-e.server.Done():
		return !e.stopped
	default:
		return false
	}
}

// Broker manages multiple IMDS server instances keyed by profile:region.
type Broker struct {
	mu        sync.Mutex
	servers   map[string]*entry // profile:region → entry
	urlIndex  map[string]*entry // URL → entry (indexes both local and docker URLs)
	gatewayIP string
	factory   ServerFactory
	logger    *slog.Logger
}

// New creates a Broker, discovering the Docker bridge gateway IP at startup.
// Discovery failure is non-fatal; the broker continues with localhost-only mode.
func New(ctx context.Context, opts Options) (*Broker, error) {
	gatewayIP := discoverDockerGateway(ctx, opts.Executor, opts.Logger)
	return &Broker{
		servers:   make(map[string]*entry),
		urlIndex:  make(map[string]*entry),
		gatewayIP: gatewayIP,
		factory:   opts.ServerFactory,
		logger:    opts.Logger,
	}, nil
}

// CreateServer starts (or returns the existing) IMDS server for the given
// profile and region. If the existing server has crashed it is replaced.
func (b *Broker) CreateServer(ctx context.Context, profile, region string) (CreateResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := profile + ":" + region

	if e, ok := b.servers[key]; ok {
		if !e.isCrashed() {
			return CreateResult{LocalURL: e.localURL, DockerURL: e.dockerURL}, nil
		}
		b.logger.Info("broker: replacing crashed server", "key", key)
		b.cleanupEntry(e)
	}

	srv, err := b.factory(ctx, profile, region, b.buildBindAddrs(), b.logger)
	if err != nil {
		return CreateResult{}, err
	}

	e := b.newEntry(srv, key)
	b.servers[key] = e
	b.urlIndex[e.localURL] = e
	if e.dockerURL != "" {
		b.urlIndex[e.dockerURL] = e
	}

	return CreateResult{LocalURL: e.localURL, DockerURL: e.dockerURL}, nil
}

// buildBindAddrs returns the bind addresses for a new server: always localhost,
// plus the Docker gateway IP when one was discovered.
func (b *Broker) buildBindAddrs() []string {
	addrs := []string{"127.0.0.1:0"}
	if b.gatewayIP != "" {
		addrs = append(addrs, b.gatewayIP+":0")
	}
	return addrs
}

// newEntry constructs an entry from a running server, resolving the Docker URL
// when a gateway is configured and the server bound a second listener.
func (b *Broker) newEntry(srv Server, key string) *entry {
	urls := srv.URLs()
	e := &entry{
		server:   srv,
		localURL: urls[0],
		key:      key,
	}
	if b.gatewayIP != "" && len(urls) > 1 {
		if dURL, err := toDockerURL(urls[1]); err == nil {
			e.dockerURL = dURL
		} else {
			b.logger.Warn("broker: failed to build docker URL", "error", err)
		}
	}
	return e
}

// StopServer stops the server matching serverURL and removes it from the
// registry. Returns an error if no server matches.
func (b *Broker) StopServer(_ context.Context, serverURL string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.urlIndex[serverURL]
	if !ok {
		return fmt.Errorf("broker: no server with URL %s", serverURL)
	}

	e.stopped = true
	e.server.Stop()
	b.cleanupEntry(e)

	return nil
}

// cleanupEntry removes an entry from both registries. Must be called with b.mu held.
func (b *Broker) cleanupEntry(e *entry) {
	delete(b.servers, e.key)
	delete(b.urlIndex, e.localURL)
	if e.dockerURL != "" {
		delete(b.urlIndex, e.dockerURL)
	}
}

// toDockerURL converts a gateway-bound URL (http://172.17.0.1:PORT) to the
// Docker-friendly form (http://host.docker.internal:PORT).
func toDockerURL(gatewayURL string) (string, error) {
	u, err := url.Parse(gatewayURL)
	if err != nil {
		return "", fmt.Errorf("broker: parse gateway URL %q: %w", gatewayURL, err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", fmt.Errorf("broker: split host:port from %q: %w", u.Host, err)
	}
	return "http://host.docker.internal:" + port, nil
}
