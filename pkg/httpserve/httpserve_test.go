package httpserve_test

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/httpserve"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "ok")
})

func listen(t *testing.T, addr string) net.Listener {
	t.Helper()
	ln, err := httpserve.Listen(addr)
	require.NoError(t, err)
	return ln
}

func start(t *testing.T, logger *slog.Logger, listeners ...net.Listener) *httpserve.Server {
	t.Helper()
	srv := httpserve.Start(okHandler, listeners, logger)
	t.Cleanup(func() {
		srv.Stop()
		<-srv.Done()
	})
	return srv
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func get(t *testing.T, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func waitDone(t *testing.T, srv *httpserve.Server) {
	t.Helper()
	select {
	case <-srv.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done() not closed")
	}
}

func TestListen_IPv4WildcardStaysIPv4(t *testing.T) {
	ln := listen(t, "0.0.0.0:0")
	defer func() { _ = ln.Close() }()
	assert.True(t, strings.HasPrefix(ln.Addr().String(), "0.0.0.0:"), ln.Addr().String())
}

func TestStart_ServesAndReportsLoopbackURL(t *testing.T) {
	srv := start(t, discard(), listen(t, "0.0.0.0:0"))

	require.Len(t, srv.URLs(), 1)
	assert.True(t, strings.HasPrefix(srv.URLs()[0], "http://127.0.0.1:"), srv.URLs()[0])
	require.NoError(t, get(t, srv.URLs()[0]))
}

func TestStop_IsIdempotentAndClosesListeners(t *testing.T) {
	srv := httpserve.Start(okHandler, []net.Listener{listen(t, "127.0.0.1:0")}, discard())
	url := srv.URLs()[0]

	srv.Stop()
	srv.Stop()
	waitDone(t, srv)

	assert.Error(t, get(t, url))
}

func TestStart_ListenerFailureStopsAllListeners(t *testing.T) {
	failing := listen(t, "127.0.0.1:0")
	srv := start(t, discard(), failing, listen(t, "127.0.0.1:0"))

	require.NoError(t, failing.Close())
	waitDone(t, srv)

	assert.Error(t, get(t, srv.URLs()[1]), "sibling listener should stop too")
}

// panicListener panics in Accept once its first connection arrives.
type panicListener struct{ net.Listener }

func (l panicListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil {
		return nil, err
	}
	panic("induced serve loop panic")
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestStart_PanicIsRecoveredAndIsolated(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	victim := start(t, logger, panicListener{listen(t, "127.0.0.1:0")})
	bystander := start(t, logger, listen(t, "127.0.0.1:0"))

	_ = get(t, victim.URLs()[0])
	waitDone(t, victim)

	assert.Contains(t, logs.String(), "panic recovered")
	assert.Contains(t, logs.String(), "induced serve loop panic")
	require.NoError(t, get(t, bystander.URLs()[0]), "other servers must keep serving")
}
