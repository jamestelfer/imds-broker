package broker_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/jamestelfer/imds-broker/pkg/broker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- test doubles ----

type fakeServer struct {
	urls []string
	done chan struct{}
}

func newFakeServer(urls ...string) *fakeServer {
	return &fakeServer{urls: urls, done: make(chan struct{})}
}

func (f *fakeServer) URLs() []string { return f.urls }

func (f *fakeServer) Stop() {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

func (f *fakeServer) Done() <-chan struct{} { return f.done }

// crash simulates an unexpected server crash (done closes without Stop).
func (f *fakeServer) crash() {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

// fakeFactory returns a ServerFactory that returns servers from the provided map
// keyed by "profile:region". If a key is missing it returns an error.
func fakeFactory(servers map[string]*fakeServer) broker.ServerFactory {
	return func(_ context.Context, profile, region string, _ []string, _ *slog.Logger) (broker.Server, error) {
		key := profile + ":" + region
		srv, ok := servers[key]
		if !ok {
			return nil, errors.New("broker: unknown profile: " + profile)
		}
		return srv, nil
	}
}

// fakeExecutor implements CommandExecutor for Docker discovery tests.
type fakeExecutor struct {
	output []byte
	err    error
}

func (f *fakeExecutor) Execute(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return f.output, f.err
}

func noDockerExecutor() *fakeExecutor {
	return &fakeExecutor{err: errors.New("docker: not found")}
}

func dockerExecutor(gatewayIP string) *fakeExecutor {
	return &fakeExecutor{output: []byte(gatewayIP + "\n")}
}

// discardLogger returns a no-op slog logger.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// ---- tests ----

// Test 1 (tracer bullet): CreateServer returns the server's localhost URL.
func TestCreateServer_ReturnsLocalURL(t *testing.T) {
	srv := newFakeServer("http://127.0.0.1:12345")
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: fakeFactory(map[string]*fakeServer{"prod:us-east-1": srv}),
	})
	require.NoError(t, err)

	result, err := b.CreateServer(context.Background(), "prod", "us-east-1")

	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:12345", result.LocalURL)
	assert.Empty(t, result.DockerURL)
}

// Test 2: CreateServer with same profile+region returns the same server (deduplication).
func TestCreateServer_Deduplication(t *testing.T) {
	srv := newFakeServer("http://127.0.0.1:12345")
	calls := 0
	factory := func(_ context.Context, _, _ string, _ []string, _ *slog.Logger) (broker.Server, error) {
		calls++
		return srv, nil
	}
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: factory,
	})
	require.NoError(t, err)

	r1, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)

	r2, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)

	assert.Equal(t, r1.LocalURL, r2.LocalURL)
	assert.Equal(t, 1, calls, "factory should only be called once")
}

// Test 3: CreateServer replaces a crashed server with a fresh one.
func TestCreateServer_ReplacesCrashedServer(t *testing.T) {
	first := newFakeServer("http://127.0.0.1:11111")
	second := newFakeServer("http://127.0.0.1:22222")
	callCount := 0
	factory := func(_ context.Context, _, _ string, _ []string, _ *slog.Logger) (broker.Server, error) {
		callCount++
		if callCount == 1 {
			return first, nil
		}
		return second, nil
	}
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: factory,
	})
	require.NoError(t, err)

	r1, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:11111", r1.LocalURL)

	// Simulate a crash (done channel closes without Stop).
	first.crash()

	r2, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:22222", r2.LocalURL)
	assert.Equal(t, 2, callCount)
}

// Test 4: StopServer stops the server and removes it from the registry.
func TestStopServer_ByLocalURL(t *testing.T) {
	srv := newFakeServer("http://127.0.0.1:12345")
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: fakeFactory(map[string]*fakeServer{"prod:us-east-1": srv}),
	})
	require.NoError(t, err)

	_, err = b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)

	err = b.StopServer(context.Background(), "http://127.0.0.1:12345")
	require.NoError(t, err)

	select {
	case <-srv.Done():
		// stopped
	default:
		t.Fatal("expected server to be stopped")
	}
}

// Test 5: StopServer with an unknown URL returns an error.
func TestStopServer_UnknownURL_ReturnsError(t *testing.T) {
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: fakeFactory(nil),
	})
	require.NoError(t, err)

	err = b.StopServer(context.Background(), "http://127.0.0.1:99999")
	require.Error(t, err)
}

// Test 6: StopServer removes the entry so a subsequent CreateServer starts fresh.
func TestStopServer_AllowsReuseOfKey(t *testing.T) {
	first := newFakeServer("http://127.0.0.1:11111")
	second := newFakeServer("http://127.0.0.1:22222")
	callCount := 0
	factory := func(_ context.Context, _, _ string, _ []string, _ *slog.Logger) (broker.Server, error) {
		callCount++
		if callCount == 1 {
			return first, nil
		}
		return second, nil
	}
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: factory,
	})
	require.NoError(t, err)

	r1, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)

	err = b.StopServer(context.Background(), r1.LocalURL)
	require.NoError(t, err)

	r2, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:22222", r2.LocalURL)
	assert.Equal(t, 2, callCount)
}

// Test 7: Factory returning an error propagates to the caller.
func TestCreateServer_FactoryError_PropagatesError(t *testing.T) {
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: fakeFactory(nil), // empty map → always errors
	})
	require.NoError(t, err)

	_, err = b.CreateServer(context.Background(), "nonexistent", "us-east-1")
	require.Error(t, err)
}

// Test 8: Docker gateway discovery succeeds → server gets two URLs, DockerURL returned.
func TestCreateServer_DockerGateway_ReturnsBothURLs(t *testing.T) {
	// The broker passes two bind addresses when gateway is discovered.
	// The fake server returns two URLs: localhost and gateway.
	var capturedBindAddrs []string
	srv := newFakeServer("http://127.0.0.1:11111", "http://172.17.0.1:22222")
	factory := func(_ context.Context, _, _ string, bindAddrs []string, _ *slog.Logger) (broker.Server, error) {
		capturedBindAddrs = bindAddrs
		return srv, nil
	}
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      dockerExecutor("172.17.0.1"),
		ServerFactory: factory,
	})
	require.NoError(t, err)

	result, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)

	assert.Equal(t, "http://127.0.0.1:11111", result.LocalURL)
	assert.Equal(t, "http://host.docker.internal:22222", result.DockerURL)
	require.Len(t, capturedBindAddrs, 2)
	assert.Equal(t, "127.0.0.1:0", capturedBindAddrs[0])
	assert.Equal(t, "172.17.0.1:0", capturedBindAddrs[1])
}

// Test 9: Docker gateway discovery fails → only localhost URL returned.
func TestCreateServer_NoDockerGateway_OnlyLocalURL(t *testing.T) {
	var capturedBindAddrs []string
	srv := newFakeServer("http://127.0.0.1:11111")
	factory := func(_ context.Context, _, _ string, bindAddrs []string, _ *slog.Logger) (broker.Server, error) {
		capturedBindAddrs = bindAddrs
		return srv, nil
	}
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      noDockerExecutor(),
		ServerFactory: factory,
	})
	require.NoError(t, err)

	result, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)

	assert.Equal(t, "http://127.0.0.1:11111", result.LocalURL)
	assert.Empty(t, result.DockerURL)
	require.Len(t, capturedBindAddrs, 1)
	assert.Equal(t, "127.0.0.1:0", capturedBindAddrs[0])
}

// Test 10: StopServer by DockerURL also works (URL index includes Docker URL).
func TestStopServer_ByDockerURL(t *testing.T) {
	srv := newFakeServer("http://127.0.0.1:11111", "http://172.17.0.1:22222")
	b, err := broker.New(context.Background(), broker.Options{
		Logger:        discardLogger(),
		Executor:      dockerExecutor("172.17.0.1"),
		ServerFactory: fakeFactory(map[string]*fakeServer{"prod:us-east-1": srv}),
	})
	require.NoError(t, err)

	result, err := b.CreateServer(context.Background(), "prod", "us-east-1")
	require.NoError(t, err)
	require.NotEmpty(t, result.DockerURL)

	err = b.StopServer(context.Background(), result.DockerURL)
	require.NoError(t, err)

	select {
	case <-srv.Done():
		// stopped
	default:
		t.Fatal("expected server to be stopped")
	}
}
