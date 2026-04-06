package imdsserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const shutdownTimeout = 2 * time.Second

// Options configures an IMDS server instance.
type Options struct {
	// Profile is the AWS profile name (informational; used in logging).
	Profile string
	// Region is the AWS region served by /latest/meta-data/placement/region.
	Region string
	// PrincipalName is the identity name returned by the credential listing
	// endpoint. In production this is derived from STS GetCallerIdentity at
	// startup. Tests supply a fixed string.
	PrincipalName string
	// BindAddrs is the list of "host:port" addresses to listen on. Port 0
	// selects an ephemeral port. The first entry is always 127.0.0.1; the
	// optional second entry is the Docker gateway IP.
	BindAddrs []string
	// Logger is used for request and error logging.
	Logger *slog.Logger
	// Credentials provides AWS credentials to the credential detail endpoint.
	Credentials CredentialProvider
}

// Server is a running IMDS-compatible HTTP server.
type Server struct {
	urls []string
	stop func()
	done chan struct{}
}

// New starts an IMDS server according to opts. Each entry in opts.BindAddrs
// gets its own net.Listener sharing the same http.Handler.
func New(opts Options) (*Server, error) {
	if len(opts.BindAddrs) == 0 {
		return nil, errors.New("imdsserver: at least one bind address is required")
	}

	handler, err := newHandler(opts.Profile, opts.Region, opts.PrincipalName, opts.Logger, opts.Credentials)
	if err != nil {
		return nil, fmt.Errorf("imdsserver: build handler: %w", err)
	}

	lc := &net.ListenConfig{}
	listeners := make([]net.Listener, 0, len(opts.BindAddrs))
	for _, addr := range opts.BindAddrs {
		ln, err := lc.Listen(context.Background(), "tcp", addr)
		if err != nil {
			// Close listeners already opened before returning.
			for _, l := range listeners {
				if cerr := l.Close(); cerr != nil {
					opts.Logger.Error("imdsserver: listener cleanup error", "error", cerr)
				}
			}
			return nil, fmt.Errorf("imdsserver: listen on %s: %w", addr, err)
		}
		listeners = append(listeners, ln)
	}

	urls := make([]string, len(listeners))
	for i, ln := range listeners {
		urls[i] = "http://" + ln.Addr().String()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	servers := make([]*http.Server, len(listeners))
	for i, ln := range listeners {
		srv := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		servers[i] = srv
		go func(s *http.Server, l net.Listener) {
			if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				opts.Logger.Error("imds server error", "addr", l.Addr(), "error", err)
			}
		}(srv, ln)
	}

	// Shutdown goroutine: waits for context cancellation, then shuts down all servers.
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		for _, srv := range servers {
			if err := srv.Shutdown(shutdownCtx); err != nil {
				opts.Logger.Error("imds server shutdown error", "error", err)
			}
		}
	}()

	stopOnce := sync.Once{}
	stopFn := func() {
		stopOnce.Do(cancel)
	}

	return &Server{
		urls: urls,
		stop: stopFn,
		done: done,
	}, nil
}

// URLs returns the HTTP base URLs for each listener bound by this server.
func (s *Server) URLs() []string {
	return s.urls
}

// Stop initiates a hard shutdown. Safe to call multiple times.
func (s *Server) Stop() {
	s.stop()
}

// Done returns a channel that is closed when all listeners have stopped.
func (s *Server) Done() <-chan struct{} {
	return s.done
}
