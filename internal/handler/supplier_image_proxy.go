package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxSupplierImageBytes = 10 << 20

var supplierImageHosts = map[string]struct{}{
	"spotlightstores.com": {}, "www.spotlightstores.com": {},
	"officeworks.com.au": {}, "www.officeworks.com.au": {},
	"images-officeworks-com-australia.netdna-ssl.com": {},
	"s3-ap-southeast-2.amazonaws.com":                 {},
	"amazon.com.au":                                   {}, "www.amazon.com.au": {},
	"bunnings.com.au": {}, "www.bunnings.com.au": {},
	"altronics.com.au": {}, "www.altronics.com.au": {},
	"core-electronics.com.au": {}, "www.core-electronics.com.au": {},
	"littlebirdelectronics.com.au": {}, "www.littlebirdelectronics.com.au": {},
	"supercheapauto.com.au": {}, "www.supercheapauto.com.au": {},
	"autobarn.com.au": {}, "www.autobarn.com.au": {}, "medias.autobarn.com.au": {},
	"media.rs-online.com": {}, "docs.rs-online.com": {},
	"ae-pic-a1.aliexpress-media.com": {}, "ae01.alicdn.com": {},
	"img.alicdn.com": {}, "ae.alicdn.com": {},
}

type supplierImageClientPolicy struct {
	allowHTTP   bool
	lookup      func(context.Context, string) ([]net.IP, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func productionSupplierImageClient() *http.Client {
	return newSupplierImageClient(supplierImageClientPolicy{})
}

func newSupplierImageClient(policy supplierImageClientPolicy) *http.Client {
	lookup := policy.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			ips := make([]net.IP, 0, len(addresses))
			for _, address := range addresses {
				ips = append(ips, address.IP)
			}
			return ips, nil
		}
	}

	rawDial := policy.dialContext
	if rawDial == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		rawDial = dialer.DialContext
	}
	dialContext := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if _, ok := supplierImageHosts[strings.ToLower(strings.TrimSuffix(host, "."))]; !ok {
			return nil, fmt.Errorf("supplier image destination host is not approved")
		}
		ips, err := lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve supplier image host: %w", err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("supplier image host has no addresses")
		}
		for _, ip := range ips {
			if !isPublicSupplierImageIP(ip) {
				return nil, fmt.Errorf("supplier image host resolves to an unsafe address")
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := rawDial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}

	client := &http.Client{
		Timeout: 25 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			DisableKeepAlives:     true,
		},
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many supplier image redirects")
		}
		if _, err := validateSupplierImageURL(req.URL.String(), policy.allowHTTP); err != nil {
			return err
		}
		ips, err := lookup(req.Context(), req.URL.Hostname())
		if err != nil {
			return fmt.Errorf("resolve supplier image redirect: %w", err)
		}
		if len(ips) == 0 {
			return fmt.Errorf("supplier image redirect host has no addresses")
		}
		for _, ip := range ips {
			if !isPublicSupplierImageIP(ip) {
				return fmt.Errorf("supplier image redirect resolves to an unsafe address")
			}
		}
		return nil
	}
	return client
}

func validateSupplierImageURL(rawURL string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil || u.User != nil || u.Hostname() == "" || u.Opaque != "" {
		return nil, fmt.Errorf("invalid supplier image URL")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if _, ok := supplierImageHosts[host]; !ok {
		return nil, fmt.Errorf("supplier image host is not approved")
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return nil, fmt.Errorf("supplier images must use HTTPS")
	}
	port := u.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return nil, fmt.Errorf("invalid supplier image port")
		}
		if (u.Scheme == "https" && portNumber != 443) || (u.Scheme == "http" && !allowHTTP) {
			return nil, fmt.Errorf("supplier image port is not allowed")
		}
	}
	return u, nil
}

func isPublicSupplierImageIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	unsafeRanges := [...]string{
		"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23",
		"2001:db8::/32", "2002::/16",
	}
	for _, raw := range unsafeRanges {
		if prefix := netip.MustParsePrefix(raw); prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func fetchSupplierImage(ctx context.Context, rawURL string, client *http.Client, allowHTTP bool) ([]byte, string, error) {
	u, err := validateSupplierImageURL(rawURL, allowHTTP)
	if err != nil {
		return nil, "", err
	}
	if client == nil {
		client = productionSupplierImageClient()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/webp,image/apng,image/*,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("supplier image returned %s", resp.Status)
	}
	if resp.ContentLength > maxSupplierImageBytes {
		return nil, "", fmt.Errorf("supplier image exceeds %d bytes", maxSupplierImageBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSupplierImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || len(data) > maxSupplierImageBytes {
		return nil, "", fmt.Errorf("supplier image is empty or exceeds %d bytes", maxSupplierImageBytes)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return nil, "", fmt.Errorf("supplier response is not an image")
	}
	return data, contentType, nil
}

func (h *Handler) HandleSupplierImageProxy(w http.ResponseWriter, r *http.Request) {
	imageURL := r.URL.Query().Get("url")
	if imageURL == "" {
		http.Error(w, "Missing url parameter", http.StatusBadRequest)
		return
	}

	if _, err := validateSupplierImageURL(imageURL, false); err != nil {
		http.Error(w, "Invalid or unapproved URL", http.StatusBadRequest)
		return
	}
	data, contentType, err := fetchSupplierImage(r.Context(), imageURL, productionSupplierImageClient(), false)
	if err != nil {
		http.Error(w, "Failed to fetch image", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
