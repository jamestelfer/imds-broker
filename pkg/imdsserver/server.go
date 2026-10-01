package imdsserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/jamestelfer/imds-broker/pkg/httpserve"
)

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
	// AccountID is the AWS account ID, sourced from STS GetCallerIdentity at
	// startup. Used in the instance identity document response.
	AccountID string
	// BindAddrs is the list of "host:port" addresses to listen on. Port 0
	// selects an ephemeral port.
	BindAddrs []string
	// Logger is used for request and error logging.
	Logger *slog.Logger
	// Credentials provides AWS credentials to the credential detail endpoint.
	Credentials CredentialProvider
}

// Server is a running IMDS-compatible HTTP server.
type Server struct {
	*httpserve.Server
}

// New starts an IMDS server according to opts. Each entry in opts.BindAddrs
// gets its own net.Listener sharing the same http.Handler.
func New(opts Options) (*Server, error) {
	if len(opts.BindAddrs) == 0 {
		return nil, errors.New("imdsserver: at least one bind address is required")
	}

	listeners := make([]net.Listener, 0, len(opts.BindAddrs))
	for _, addr := range opts.BindAddrs {
		ln, err := httpserve.Listen(addr)
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return nil, fmt.Errorf("imdsserver: listen on %s: %w", addr, err)
		}
		listeners = append(listeners, newFilteredListener(ln, opts.Logger))
	}

	handler := newHandler(opts.Region, opts.PrincipalName, opts.AccountID, opts.Logger, opts.Credentials)
	return &Server{httpserve.Start(handler, listeners, opts.Logger)}, nil
}
