// Package containercreds implements an AWS container credentials provider
// endpoint, the protocol SDKs consume through
// AWS_CONTAINER_CREDENTIALS_FULL_URI and AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE.
package containercreds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const shutdownTimeout = 2 * time.Second

// DefaultPath is the conventional credentials path, carrying the API-version
// segment AWS credential endpoints use. SDKs do not require it; the operator
// supplies the full URL.
const DefaultPath = "/2016-11-01/credentials"

// Options configures a container credentials server instance.
type Options struct {
	// Profile is the AWS profile name (informational; used in logging).
	Profile string
	// Region is the AWS region of the profile (informational; used in logging).
	Region string
	// BindAddr is the "host:port" address to listen on. Port 0 selects an
	// ephemeral port.
	BindAddr string
	// Path is the credentials path. Defaults to DefaultPath.
	Path string
	// Logger is used for request and error logging.
	Logger *slog.Logger
	// Credentials provides the AWS credentials served at Path.
	Credentials CredentialProvider
}

// Server is a running container credentials HTTP server.
type Server struct {
	addr  string
	url   string
	path  string
	token []byte
	stop  func()
	done  chan struct{}
}

// New binds a listener, generates an authorisation token, and starts serving
// the credentials returned by opts.Credentials.
func New(opts Options) (*Server, error) {
	if opts.BindAddr == "" {
		return nil, errors.New("containercreds: bind address is required")
	}
	if opts.Credentials == nil {
		return nil, errors.New("containercreds: credential provider is required")
	}
	credsPath := opts.Path
	if credsPath == "" {
		credsPath = DefaultPath
	}
	if err := validatePath(credsPath); err != nil {
		return nil, err
	}

	token, err := newToken()
	if err != nil {
		return nil, err
	}

	logger := opts.Logger.With("profile", opts.Profile, "region", opts.Region)

	lc := &net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", opts.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("containercreds: listen on %s: %w", opts.BindAddr, err)
	}

	addr := ln.Addr().String()
	srv := &http.Server{
		Handler:           newHandler(credsPath, token, logger, opts.Credentials),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Any exit from Serve other than a requested shutdown cancels the
	// context, so Done() closes and the broker can detect the crash.
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("container credentials server panic recovered",
					"addr", addr,
					"panic", fmt.Sprintf("%v", r),
					"stack", string(debug.Stack()))
			}
		}()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("container credentials server error", "addr", addr, "error", err)
		}
	}()

	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("container credentials server shutdown error", "error", err)
		}
	}()

	var stopOnce sync.Once
	return &Server{
		addr:  addr,
		url:   "http://" + localAddr(addr),
		path:  credsPath,
		token: token,
		stop:  func() { stopOnce.Do(cancel) },
		done:  done,
	}, nil
}

// validatePath rejects paths that would not register as a single exact
// ServeMux route: relative, unclean, root, or containing pattern syntax.
func validatePath(p string) error {
	switch {
	case !strings.HasPrefix(p, "/"), p == "/", path.Clean(p) != p,
		strings.ContainsAny(p, "{} \t\r\n"):
		return fmt.Errorf("containercreds: invalid credentials path %q", p)
	}
	return nil
}

// localAddr maps an all-interfaces listen address to loopback so the
// resulting URL is dialable from the host.
func localAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "0.0.0.0":
		return net.JoinHostPort("127.0.0.1", port)
	case "::":
		return net.JoinHostPort("::1", port)
	}
	return addr
}

// Addr returns the bound listener address, for example "0.0.0.0:8080".
func (s *Server) Addr() string {
	return s.addr
}

// URLs returns the HTTP base URL of the listener. An all-interfaces bind is
// reported as loopback.
func (s *Server) URLs() []string {
	return []string{s.url}
}

// CredentialsURL returns the full credentials endpoint URL, suitable for
// AWS_CONTAINER_CREDENTIALS_FULL_URI when the client runs on this host.
func (s *Server) CredentialsURL() string {
	return s.url + s.path
}

// Path returns the credentials path served by this server.
func (s *Server) Path() string {
	return s.path
}

// Token returns the authorisation token clients must send verbatim in the
// Authorization header. Never log this value.
func (s *Server) Token() string {
	return string(s.token)
}

// Stop initiates a hard shutdown. Safe to call multiple times.
func (s *Server) Stop() {
	s.stop()
}

// Done returns a channel that is closed when the server has stopped.
func (s *Server) Done() <-chan struct{} {
	return s.done
}
