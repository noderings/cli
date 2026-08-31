package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// newTestClient returns a client pointed at srv with an initial token of "old".
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(&Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetToken("old")
	return c
}

// authedCall issues a request carrying whatever token the client currently holds,
// mirroring how the generated client's request editor behaves.
func authedCall(ctx context.Context, c *Client, url string) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.GetToken())
		return http.DefaultClient.Do(req)
	}
}

func TestDoWithAutoRefreshRetriesWithNewToken(t *testing.T) {
	var refreshes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	c := newTestClient(t, srv)
	c.SetTokenRefreshFunc(func(context.Context) (string, error) {
		atomic.AddInt32(&refreshes, 1)
		return "new", nil
	})

	resp, err := c.DoWithAutoRefresh(ctx, 3, authedCall(ctx, c, srv.URL))
	if err != nil {
		t.Fatalf("DoWithAutoRefresh: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Fatalf("refreshes = %d, want 1", got)
	}
	if c.GetToken() != "new" {
		t.Fatalf("token = %q, want %q", c.GetToken(), "new")
	}
}

// A refreshed token does not mean the retried call succeeded: a non-401 failure on the
// second attempt must surface as an error instead of a successful-looking response.
func TestDoWithAutoRefreshSurfacesPostRefreshFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"server error", http.StatusInternalServerError},
		{"forbidden", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer new" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			ctx := context.Background()
			c := newTestClient(t, srv)
			c.SetTokenRefreshFunc(func(context.Context) (string, error) { return "new", nil })

			resp, err := c.DoWithAutoRefresh(ctx, 1, authedCall(ctx, c, srv.URL))
			if err == nil {
				_ = resp.Body.Close()
				t.Fatalf("expected error for status %d, got response %d", tc.status, resp.StatusCode)
			}
			if resp != nil {
				t.Fatalf("expected nil response alongside error, got %d", resp.StatusCode)
			}
		})
	}
}

func TestDoWithAutoRefreshUnrecoverableWhenRefreshFails(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx := context.Background()
	c := newTestClient(t, srv)
	c.SetTokenRefreshFunc(func(context.Context) (string, error) {
		return "", http.ErrNoCookie
	})

	if _, err := c.DoWithAutoRefresh(ctx, 3, authedCall(ctx, c, srv.URL)); err == nil {
		t.Fatal("expected error when refresh fails")
	}
	// A failed refresh must not be retried: exactly one request reaches the server.
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server calls = %d, want 1", got)
	}
}

// Concurrent 401s must trigger exactly one refresh, and the losing callers must retry
// with the refreshed token rather than racing ahead with the stale one.
func TestDoWithAutoRefreshConcurrentRefreshesOnce(t *testing.T) {
	const callers = 6

	var refreshes int32
	var mu sync.Mutex
	staleSeen := 0
	allStale := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer new" {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		staleSeen++
		if staleSeen == callers {
			close(allStale)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx := context.Background()
	c := newTestClient(t, srv)
	c.SetTokenRefreshFunc(func(context.Context) (string, error) {
		// Hold the refresh until every caller has seen a 401 with the stale token, so
		// all of them contend on the refresh lock.
		<-allStale
		atomic.AddInt32(&refreshes, 1)
		return "new", nil
	})

	var wg sync.WaitGroup
	errs := make([]error, callers)
	codes := make([]int, callers)
	for i := range callers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := c.DoWithAutoRefresh(ctx, 3, authedCall(ctx, c, srv.URL))
			errs[idx] = err
			if resp != nil {
				codes[idx] = resp.StatusCode
				_ = resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if codes[i] != http.StatusOK {
			t.Fatalf("caller %d: status = %d, want 200", i, codes[i])
		}
	}
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Fatalf("refreshes = %d, want exactly 1", got)
	}
}
