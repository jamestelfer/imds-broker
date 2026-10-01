package broker_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/jamestelfer/imds-broker/pkg/containercreds"
)

// failMode selects how a faultListener fails on its next Accept.
type failMode int32

const (
	failNone failMode = iota
	failError
	failPanic
)

// faultListener fails the serve loop on demand. Accept blocks in the
// underlying listener, so trigger sets the mode and then dials in to wake it.
type faultListener struct {
	net.Listener
	mode atomic.Int32
}

func (l *faultListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	switch failMode(l.mode.Load()) {
	case failError:
		if conn != nil {
			_ = conn.Close()
		}
		return nil, errors.New("induced listener failure")
	case failPanic:
		if conn != nil {
			_ = conn.Close()
		}
		panic("induced serve loop panic")
	}
	return conn, err
}

func (l *faultListener) trigger(t *testing.T, mode failMode) {
	t.Helper()
	l.mode.Store(int32(mode))
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", l.Addr().String())
	if err == nil {
		_ = conn.Close()
	}
}

// containerHarness builds real containercreds servers for a broker and keeps
// handles so tests can authorise requests and induce failures.
type containerHarness struct {
	t      *testing.T
	logger *slog.Logger

	mu        sync.Mutex
	created   int
	servers   map[string]*containercreds.Server // URL → server
	listeners map[string]*faultListener         // URL → listener
}

func newContainerHarness(t *testing.T, logger *slog.Logger) *containerHarness {
	return &containerHarness{
		t:         t,
		logger:    logger,
		servers:   make(map[string]*containercreds.Server),
		listeners: make(map[string]*faultListener),
	}
}

// factory returns a broker.ServerFactory that starts container credentials
// servers.
//
// Constraint: the broker dedups on profile:region only. A second CreateServer
// for the same key returns the existing server, even if the caller wanted a
// different token file. This is acceptable while the container protocol is
// reachable only through serve, which runs a single server directly.
func (h *containerHarness) factory() broker.ServerFactory {
	return func(_ context.Context, profile, region string, bindAddrs []string, logger *slog.Logger) (broker.Server, error) {
		var fl *faultListener
		creds := aws.Credentials{
			AccessKeyID:     "AKID-" + profile,
			SecretAccessKey: "secret",
			SessionToken:    "session",
			CanExpire:       true,
			Expires:         time.Now().Add(time.Hour),
		}
		srv, err := containercreds.New(containercreds.Options{
			Profile:   profile,
			Region:    region,
			BindAddr:  bindAddrs[0],
			TokenFile: filepath.Join(h.t.TempDir(), "token"),
			WrapListener: func(ln net.Listener) net.Listener {
				fl = &faultListener{Listener: ln}
				return fl
			},
			Logger: h.logger,
			Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				return creds, nil
			}),
		})
		if err != nil {
			return nil, err
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		h.created++
		h.servers[srv.URLs()[0]] = srv
		h.listeners[srv.URLs()[0]] = fl
		h.t.Cleanup(func() {
			srv.Stop()
			<-srv.Done()
		})
		return srv, nil
	}
}

func (h *containerHarness) server(url string) *containercreds.Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.servers[url]
}

func (h *containerHarness) listener(url string) *faultListener {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listeners[url]
}

func (h *containerHarness) createdCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.created
}

// get performs an authorised credentials request against the server at url.
func (h *containerHarness) get(url string) (int, error) {
	srv := h.server(url)
	req, err := http.NewRequestWithContext(h.t.Context(), http.MethodGet, srv.CredentialsURL(), nil)
	require.NoError(h.t, err)
	req.Header.Set("Authorization", srv.Token())
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func waitDone(t *testing.T, srv broker.Server) {
	t.Helper()
	select {
	case <-srv.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done() not closed")
	}
}

func newContainerBroker(t *testing.T, logger *slog.Logger) (*broker.Broker, *containerHarness) {
	t.Helper()
	h := newContainerHarness(t, logger)
	b, err := broker.New(t.Context(), broker.Options{Logger: logger, ServerFactory: h.factory()})
	require.NoError(t, err)
	t.Cleanup(b.StopAll)
	return b, h
}

func TestContainerServer_Deduplication(t *testing.T) {
	b, h := newContainerBroker(t, discardLogger())

	r1, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	r2, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)

	assert.Equal(t, r1.LocalURL, r2.LocalURL)
	assert.Equal(t, 1, h.createdCount(), "only one listener should be bound")

	status, err := h.get(r1.LocalURL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestContainerServer_CrashIsReplaced(t *testing.T) {
	b, h := newContainerBroker(t, discardLogger())

	r1, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	crashed := h.server(r1.LocalURL)

	h.listener(r1.LocalURL).trigger(t, failError)
	waitDone(t, crashed)

	r2, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	assert.NotEqual(t, r1.LocalURL, r2.LocalURL)
	assert.Equal(t, 2, h.createdCount())

	status, err := h.get(r2.LocalURL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestContainerServer_StopServerFreesPort(t *testing.T) {
	b, h := newContainerBroker(t, discardLogger())

	r, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	srv := h.server(r.LocalURL)

	status, err := h.get(r.LocalURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)

	require.NoError(t, b.StopServer(t.Context(), r.LocalURL))
	waitDone(t, srv)

	_, err = h.get(r.LocalURL)
	require.Error(t, err)
	var opErr *net.OpError
	assert.ErrorAs(t, err, &opErr, "expected a connection failure, got %v", err)
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

func TestContainerServer_PanicIsRecoveredAndIsolated(t *testing.T) {
	var logs syncBuffer
	b, h := newContainerBroker(t, slog.New(slog.NewJSONHandler(&logs, nil)))

	victim, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	bystander, err := b.CreateServer(t.Context(), "dev", "us-east-1")
	require.NoError(t, err)

	h.listener(victim.LocalURL).trigger(t, failPanic)
	waitDone(t, h.server(victim.LocalURL))

	assert.Contains(t, logs.String(), "panic recovered")
	assert.Contains(t, logs.String(), "induced serve loop panic")

	status, err := h.get(bystander.LocalURL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status, "other servers must keep serving")

	replaced, err := b.CreateServer(t.Context(), "prod", "us-east-1")
	require.NoError(t, err)
	assert.NotEqual(t, victim.LocalURL, replaced.LocalURL)
	status, err = h.get(replaced.LocalURL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}
