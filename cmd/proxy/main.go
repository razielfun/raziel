// Raziel connect proxy — routes *.razi.lol to agent VPS private IPs.
//
// Each incoming request has a Host header like "my-server.connect.razi.lol".
// The proxy strips the base domain, looks up the server name in a routing table
// refreshed from the web app every 10s, and reverse-proxies (HTTP + WebSocket)
// to the target VPS on port 8000.
//
// Environment variables:
//
//	APP_URL        Web app base URL (e.g. https://raziel-web.vercel.app)
//	PROXY_SECRET   Shared secret for /api/proxy/routing
//	BASE_DOMAIN    Base domain to strip (default: connect.razi.lol)
//	LISTEN_ADDR    Listen address (default: :80)
//	AGENT_PORT     Target port on each VPS (default: 8000)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

func main() {
	appURL := mustEnv("APP_URL")
	proxySecret := mustEnv("PROXY_SECRET")
	baseDomain := envOr("BASE_DOMAIN", "razi.lol")
	listenAddr := envOr("LISTEN_ADDR", ":80")
	agentPort := envOr("AGENT_PORT", "8000")

	table := &routingTable{}

	// Initial fetch — fail fast if the web app is unreachable
	if err := table.refresh(appURL, proxySecret); err != nil {
		log.Fatalf("initial routing table fetch failed: %v", err)
	}
	log.Printf("proxy starting on %s — base domain: %s", listenAddr, baseDomain)

	// Background refresh every 10s
	go func() {
		for range time.Tick(10 * time.Second) {
			if err := table.refresh(appURL, proxySecret); err != nil {
				log.Printf("routing table refresh error: %v", err)
			}
		}
	}()

	handler := &proxyHandler{
		table:      table,
		baseDomain: baseDomain,
		agentPort:  agentPort,
	}

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // disabled — WebSocket connections are long-lived
		IdleTimeout:  120 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

// routingTable holds name → private IP mappings, refreshed from the web app.
type routingTable struct {
	mu     sync.RWMutex
	routes map[string]string // server name → private IP
}

func (t *routingTable) lookup(name string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ip, ok := t.routes[name]
	return ip, ok
}

func (t *routingTable) refresh(appURL, secret string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", appURL+"/api/proxy/routing", nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-proxy-secret", secret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("routing API returned %d", resp.StatusCode)
	}

	var body struct {
		Routes map[string]string `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}

	t.mu.Lock()
	t.routes = body.Routes
	t.mu.Unlock()

	log.Printf("routing table refreshed: %d servers", len(body.Routes))
	return nil
}

// proxyHandler routes requests by Host header subdomain.
type proxyHandler struct {
	table      *routingTable
	baseDomain string
	agentPort  string
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	// Strip port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	suffix := "." + h.baseDomain
	if !strings.HasSuffix(host, suffix) {
		http.Error(w, "unknown host", http.StatusBadGateway)
		return
	}

	name := strings.TrimSuffix(host, suffix)
	if name == "" {
		http.Error(w, "missing server name", http.StatusBadGateway)
		return
	}

	ip, ok := h.table.lookup(name)
	if !ok {
		http.Error(w, fmt.Sprintf("server %q not found", name), http.StatusNotFound)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(ip, h.agentPort),
	}

	// WebSocket: manual tunnel
	if isWebSocket(r) {
		h.tunnelWS(w, r, target)
		return
	}

	// HTTP: standard reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error → %s: %v", target.Host, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
	}
	// Preserve original path and query
	r.URL.Host = target.Host
	r.URL.Scheme = target.Scheme
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Forwarded-For", r.RemoteAddr)
	proxy.ServeHTTP(w, r)
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// tunnelWS does a raw TCP tunnel for WebSocket upgrades.
func (h *proxyHandler) tunnelWS(w http.ResponseWriter, r *http.Request, target *url.URL) {
	// Connect to the upstream
	upstreamConn, err := net.DialTimeout("tcp", target.Host, 10*time.Second)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer upstreamConn.Close()

	// Forward the original HTTP upgrade request upstream
	if err := r.Write(upstreamConn); err != nil {
		log.Printf("ws tunnel: write request: %v", err)
		return
	}

	// Hijack the client connection
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		log.Printf("ws tunnel: hijack: %v", err)
		return
	}
	defer clientConn.Close()

	// Bidirectional copy
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}

	go cp(upstreamConn, clientConn)
	go cp(clientConn, upstreamConn)
	<-done
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
