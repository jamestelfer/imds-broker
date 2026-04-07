package broker

import (
	"context"
	"fmt"
	"log/slog"
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
	LocalURL string
}

// Options configures a Broker.
type Options struct {
	Logger        *slog.Logger
	ServerFactory ServerFactory
}

// entry tracks a running IMDS server instance and its registered URL.
type entry struct {
	server   Server
	localURL string
	key      string
	stopped  bool // true when the broker itself called Stop
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
	mu       sync.Mutex
	servers  map[string]*entry // profile:region → entry
	urlIndex map[string]*entry // URL → entry
	factory  ServerFactory
	logger   *slog.Logger
}

// New creates a Broker.
func New(_ context.Context, opts Options) (*Broker, error) {
	return &Broker{
		servers:  make(map[string]*entry),
		urlIndex: make(map[string]*entry),
		factory:  opts.ServerFactory,
		logger:   opts.Logger,
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
			return CreateResult{LocalURL: e.localURL}, nil
		}
		b.logger.Info("broker: replacing crashed server", "key", key)
		b.cleanupEntry(e)
	}

	srv, err := b.factory(ctx, profile, region, b.buildBindAddrs(), b.logger)
	if err != nil {
		return CreateResult{}, err
	}

	e := &entry{
		server:   srv,
		localURL: srv.URLs()[0],
		key:      key,
	}
	b.servers[key] = e
	b.urlIndex[e.localURL] = e

	return CreateResult{LocalURL: e.localURL}, nil
}

// buildBindAddrs returns the bind addresses for a new server.
// Binds to 0.0.0.0 so the server is reachable from all interfaces including
// Docker containers on the host network.
func (b *Broker) buildBindAddrs() []string {
	return []string{"0.0.0.0:0"}
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

// StopAll stops every running server and clears the registry. Safe to call
// multiple times. Intended for broker shutdown on process exit.
func (b *Broker) StopAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.servers {
		e.stopped = true
		e.server.Stop()
	}
	b.servers = make(map[string]*entry)
	b.urlIndex = make(map[string]*entry)
}

// cleanupEntry removes an entry from the registries. Must be called with b.mu held.
func (b *Broker) cleanupEntry(e *entry) {
	delete(b.servers, e.key)
	delete(b.urlIndex, e.localURL)
}
