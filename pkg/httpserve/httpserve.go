// Package httpserve runs an http.Handler on one or more listeners with a hard
// stop and crash detection, for the broker's credential servers.
package httpserve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"
)

const shutdownTimeout = 2 * time.Second

// Listen binds a TCP listener on addr. An IPv4 literal host listens on tcp4,
// so "0.0.0.0" binds IPv4 only and reports itself as 0.0.0.0 rather than as
// a dual-stack "[::]" listener.
func Listen(addr string) (net.Listener, error) {
	network := "tcp"
	if host, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			network = "tcp4"
		}
	}
	return (&net.ListenConfig{}).Listen(context.Background(), network, addr)
}

// Server is a running set of HTTP listeners sharing one handler.
type Server struct {
	urls []string
	stop func()
	done chan struct{}
}

// Start serves handler on each listener. If any serve loop exits other than
// through Stop, including by a recovered panic, the whole server stops, so
// Done() closes and the broker can replace it.
func Start(handler http.Handler, listeners []net.Listener, logger *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		urls: make([]string, len(listeners)),
		stop: sync.OnceFunc(cancel),
		done: make(chan struct{}),
	}

	servers := make([]*http.Server, len(listeners))
	for i, ln := range listeners {
		s.urls[i] = "http://" + localAddr(ln.Addr().String())
		servers[i] = &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go serve(servers[i], ln, cancel, logger)
	}

	go func() {
		defer close(s.done)
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		for _, srv := range servers {
			if err := srv.Shutdown(shutdownCtx); err != nil {
				logger.Error("http server shutdown error", "error", err)
			}
		}
	}()

	return s
}

func serve(srv *http.Server, ln net.Listener, cancel func(), logger *slog.Logger) {
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("http server panic recovered",
				"addr", ln.Addr(),
				"panic", fmt.Sprint(r),
				"stack", string(debug.Stack()))
		}
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server error", "addr", ln.Addr(), "error", err)
	}
}

// localAddr maps an all-interfaces address to loopback, so the URL is
// dialable from the host.
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

// URLs returns the base URL of each listener, with all-interfaces binds
// reported as loopback.
func (s *Server) URLs() []string {
	return s.urls
}

// Stop initiates a hard shutdown. Safe to call multiple times.
func (s *Server) Stop() {
	s.stop()
}

// Done returns a channel that is closed when every listener has stopped.
func (s *Server) Done() <-chan struct{} {
	return s.done
}
