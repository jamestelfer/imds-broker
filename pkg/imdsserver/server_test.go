package imdsserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/jamestelfer/imds-broker/pkg/imdsserver"
)

type staticCreds struct {
	creds aws.Credentials
}

func (s *staticCreds) Retrieve(_ context.Context) (aws.Credentials, error) {
	return s.creds, nil
}

func newServerCreds() *staticCreds {
	return &staticCreds{
		creds: aws.Credentials{
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			SessionToken:    "AQoDYXdzEJr//fake-session-token",
			Expires:         time.Now().UTC().Add(time.Hour),
		},
	}
}

func TestServer_StartsAndResponds(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := imdsserver.New(imdsserver.Options{
		Profile:       "test",
		Region:        "us-east-1",
		PrincipalName: "TestRole",
		BindAddrs:     []string{"127.0.0.1:0"},
		Logger:        logger,
		Credentials:   newServerCreds(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Stop()

	urls := srv.URLs()
	if len(urls) == 0 {
		t.Fatal("expected at least one URL")
	}
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL for 1 bind addr, got %d", len(urls))
	}

	// The server should respond to HTTP requests.
	url := urls[0]
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url+"/latest/meta-data/placement/region", nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("resp.Body.Close: %v", err)
	}
	// 401 because we didn't include a token — but the server is alive.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 (no token), got %d", resp.StatusCode)
	}
}

func TestServer_DoneClosesOnStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := imdsserver.New(imdsserver.Options{
		Profile:       "test",
		Region:        "us-east-1",
		PrincipalName: "TestRole",
		BindAddrs:     []string{"127.0.0.1:0"},
		Logger:        logger,
		Credentials:   newServerCreds(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	srv.Stop()

	select {
	case <-srv.Done():
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("Done() channel not closed after Stop()")
	}
}

func TestServer_MultipleBindAddrs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := imdsserver.New(imdsserver.Options{
		Profile:       "test",
		Region:        "us-west-2",
		PrincipalName: "TestRole",
		BindAddrs:     []string{"127.0.0.1:0", "127.0.0.1:0"},
		Logger:        logger,
		Credentials:   newServerCreds(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Stop()

	urls := srv.URLs()
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs for 2 bind addrs, got %d: %v", len(urls), urls)
	}
	if urls[0] == urls[1] {
		t.Error("expected distinct URLs for distinct listeners")
	}
}

func TestServer_StopIsIdempotent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := imdsserver.New(imdsserver.Options{
		Profile:       "test",
		Region:        "us-east-1",
		PrincipalName: "TestRole",
		BindAddrs:     []string{"127.0.0.1:0"},
		Logger:        logger,
		Credentials:   newServerCreds(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	srv.Stop()
	srv.Stop() // must not panic

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case <-srv.Done():
	case <-ctx.Done():
		t.Fatal("Done() not closed within timeout")
	}
}
