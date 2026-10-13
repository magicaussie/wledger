package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"

	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func localSupplierImageURL(t *testing.T, host string, server *httptest.Server, path string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://%s:%s%s", host, port, path)
}

func localSupplierImageClient(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	ports := make(map[string]string, len(servers))
	for _, server := range servers {
		_, port, err := net.SplitHostPort(server.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		ports[port] = server.Listener.Addr().String()
	}
	return newSupplierImageClient(supplierImageClientPolicy{
		allowHTTP: true,
		lookup: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			local, ok := ports[port]
			if !ok {
				return nil, fmt.Errorf("no test server for port %s", port)
			}
			return (&net.Dialer{}).DialContext(ctx, network, local)
		},
	})
}

func TestFetchSupplierImageFollowsOnlyApprovedSafeRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png bytes"))
	}))
	defer target.Close()

	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, localSupplierImageURL(t, "media.rs-online.com", target, "/image.png"), http.StatusFound)
	}))
	defer start.Close()
	client := localSupplierImageClient(t, start, target)

	data, contentType, err := fetchSupplierImage(context.Background(), localSupplierImageURL(t, "officeworks.com.au", start, "/start"), client, true)
	if err != nil {
		t.Fatalf("fetch redirected image: %v", err)
	}
	if string(data) != "png bytes" || contentType != "image/png" {
		t.Fatalf("fetch result = (%q, %q), want image bytes and image/png", data, contentType)
	}
	if got := targetHits.Load(); got != 1 {
		t.Fatalf("redirect target hits = %d, want 1", got)
	}
}

func TestFetchSupplierImageRejectsUnapprovedRedirectHost(t *testing.T) {
	var unexpectedHits atomic.Int32
	unexpected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unexpectedHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer unexpected.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, localSupplierImageURL(t, "127.0.0.1", unexpected, "/private"), http.StatusFound)
	}))
	defer start.Close()

	_, _, err := fetchSupplierImage(context.Background(), localSupplierImageURL(t, "officeworks.com.au", start, "/start"), localSupplierImageClient(t, start, unexpected), true)
	if err == nil {
		t.Fatal("expected unapproved redirect to be rejected")
	}
	if got := unexpectedHits.Load(); got != 0 {
		t.Fatalf("unapproved redirect destination received %d requests", got)
	}
}

func TestFetchSupplierImageRejectsPrivateRedirectResolution(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, localSupplierImageURL(t, "media.rs-online.com", target, "/private"), http.StatusFound)
	}))
	defer start.Close()

	ports := map[string]string{}
	for _, server := range []*httptest.Server{start, target} {
		_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
		ports[port] = server.Listener.Addr().String()
	}
	client := newSupplierImageClient(supplierImageClientPolicy{
		allowHTTP: true,
		lookup: func(_ context.Context, host string) ([]net.IP, error) {
			if host == "media.rs-online.com" {
				return []net.IP{net.ParseIP("10.0.0.8")}, nil
			}
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(address)
			return (&net.Dialer{}).DialContext(ctx, network, ports[port])
		},
	})
	_, _, err := fetchSupplierImage(context.Background(), localSupplierImageURL(t, "officeworks.com.au", start, "/start"), client, true)
	if err == nil {
		t.Fatal("expected redirect resolving to private IP to be rejected")
	}
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("private redirect destination received %d requests", got)
	}
}

func TestFetchSupplierImageRejectsDNSRebindingAtDialTime(t *testing.T) {
	var lookups atomic.Int32
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, localSupplierImageURL(t, "media.rs-online.com", target, "/rebinding"), http.StatusFound)
	}))
	defer start.Close()
	ports := map[string]string{}
	for _, server := range []*httptest.Server{start, target} {
		_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
		ports[port] = server.Listener.Addr().String()
	}
	client := newSupplierImageClient(supplierImageClientPolicy{
		allowHTTP: true,
		lookup: func(_ context.Context, host string) ([]net.IP, error) {
			if host != "media.rs-online.com" {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			if lookups.Add(1) == 1 {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(address)
			return (&net.Dialer{}).DialContext(ctx, network, ports[port])
		},
	})
	_, _, err := fetchSupplierImage(context.Background(), localSupplierImageURL(t, "officeworks.com.au", start, "/start"), client, true)
	if err == nil {
		t.Fatal("expected DNS-rebound destination to be rejected")
	}
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("DNS-rebound destination received %d requests", got)
	}
}

func TestFetchSupplierImageRejectsOversizeAndNonImageResponses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "oversize", contentType: "image/png", body: strings.Repeat("x", maxSupplierImageBytes+1)},
		{name: "not image", contentType: "text/html", body: "<html>no</html>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, _, err := fetchSupplierImage(context.Background(), localSupplierImageURL(t, "officeworks.com.au", server, "/image"), localSupplierImageClient(t, server), true)
			if err == nil {
				t.Fatal("expected response to be rejected")
			}
		})
	}
}

func TestFetchSupplierImageHonorsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := localSupplierImageClient(t, server)
	client.Timeout = 20 * time.Millisecond
	_, _, err := fetchSupplierImage(context.Background(), localSupplierImageURL(t, "officeworks.com.au", server, "/slow"), client, true)
	if err == nil {
		t.Fatal("expected slow response to time out")
	}
}

func TestValidateSupplierImageURLRejectsInvalidDestinations(t *testing.T) {
	cases := []string{
		"http://officeworks.com.au/image.png",
		"https://user:pass@officeworks.com.au/image.png",
		"https://127.0.0.1/image.png",
		"https://officeworks.com.au:444/image.png",
		"https://officeworks.com.au.evil.example/image.png",
		"file:///etc/passwd",
	}
	for _, raw := range cases {
		t.Run(url.QueryEscape(raw), func(t *testing.T) {
			if _, err := validateSupplierImageURL(raw, false); err == nil {
				t.Fatalf("validateSupplierImageURL(%q) unexpectedly succeeded", raw)
			}
		})
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "192.0.2.1", "::1", "fc00::1", "2001:db8::1"} {
		if isPublicSupplierImageIP(net.ParseIP(raw)) {
			t.Errorf("unsafe IP %s classified public", raw)
		}
	}
	if !isPublicSupplierImageIP(net.ParseIP("93.184.216.34")) {
		t.Fatal("public IPv4 address was rejected")
	}

}
