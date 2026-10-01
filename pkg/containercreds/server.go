// Package containercreds implements an AWS container credentials provider
// endpoint, the protocol SDKs consume through
// AWS_CONTAINER_CREDENTIALS_FULL_URI and AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE.
package containercreds

import (
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/jamestelfer/imds-broker/pkg/httpserve"
)

// Path is the credentials path. The 2016-11-01 segment follows the AWS
// credential endpoint convention; SDKs take the full URL from the operator
// and do not require it.
const Path = "/2016-11-01/credentials"

// Options configures a container credentials server instance.
type Options struct {
	// Profile and Region identify the served credentials in logs.
	Profile string
	Region  string
	// BindAddr is the "host:port" address to listen on. Port 0 selects an
	// ephemeral port.
	BindAddr string
	// TokenFile, when set, receives the authorisation token after the
	// listener binds, so a present, non-empty file signals readiness. It is
	// removed on shutdown if it still holds this server's token.
	TokenFile string
	Logger    *slog.Logger
	// Credentials provides the AWS credentials served at Path.
	Credentials CredentialProvider
}

// Server is a running container credentials HTTP server.
type Server struct {
	http  *httpserve.Server
	addr  string
	token []byte
	done  chan struct{}
}

// New binds a listener, generates an authorisation token, and starts serving
// the credentials returned by opts.Credentials.
func New(opts Options) (*Server, error) {
	if opts.Credentials == nil {
		return nil, errors.New("containercreds: credential provider is required")
	}

	token, err := newToken()
	if err != nil {
		return nil, err
	}

	ln, err := httpserve.Listen(opts.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("containercreds: listen on %s: %w", opts.BindAddr, err)
	}

	logger := opts.Logger.With("profile", opts.Profile, "region", opts.Region)
	s := &Server{
		http:  httpserve.Start(newHandler(token, logger, opts.Credentials), []net.Listener{ln}, logger),
		addr:  ln.Addr().String(),
		token: token,
		done:  make(chan struct{}),
	}

	go func() {
		defer close(s.done)
		<-s.http.Done()
		if opts.TokenFile == "" {
			return
		}
		if err := removeTokenFile(opts.TokenFile, token); err != nil {
			logger.Error("token file cleanup error", "error", err)
		}
	}()

	if opts.TokenFile != "" {
		if err := writeTokenFile(opts.TokenFile, token); err != nil {
			s.Stop()
			<-s.Done()
			return nil, err
		}
	}

	return s, nil
}

// Addr returns the bound listener address, for example "0.0.0.0:8080".
func (s *Server) Addr() string {
	return s.addr
}

// URLs returns the HTTP base URL of the listener, with an all-interfaces
// bind reported as loopback.
func (s *Server) URLs() []string {
	return s.http.URLs()
}

// CredentialsURL returns the full credentials endpoint URL for a client on
// this host.
func (s *Server) CredentialsURL() string {
	return s.http.URLs()[0] + Path
}

// Token returns the authorisation token clients send verbatim in the
// Authorization header.
func (s *Server) Token() string {
	return string(s.token)
}

// Stop initiates a hard shutdown. Safe to call multiple times.
func (s *Server) Stop() {
	s.http.Stop()
}

// Done returns a channel that is closed once the server has stopped and its
// token file has been removed.
func (s *Server) Done() <-chan struct{} {
	return s.done
}
