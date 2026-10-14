package proxy

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// MaxBodyBytes caps /v1 request bodies. The HTTP middleware enforces it; the
// proxy buffers the body to extract "model" and to allow one stale-target
// retry (§7.3 of the design).
const MaxBodyBytes = 10 << 20 // 10MB

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

type openAIErrorEnvelope struct {
	Error openAIError `json:"error"`
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func writeError(w http.ResponseWriter, status int, errType, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openAIErrorEnvelope{
		Error: openAIError{Message: message, Type: errType, Code: code},
	})
}

// RequireAuth guards /v1 routes with the coordinator api-key, accepted via
// the api-key header or — since that is what OpenAI clients send — as
// Authorization: Bearer <key>.
func (p *Router) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.authEnabled {
			next.ServeHTTP(w, r)
			return
		}

		key := r.Header.Get("api-key")
		if key == "" {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				key = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			}
		}
		if key == "" {
			writeError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key",
				"You didn't provide an API key. Send it as the api-key header or as 'Authorization: Bearer <key>'.")
			return
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(p.apiKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key",
				"Incorrect API key provided.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NotFound answers unknown /v1 endpoints in the OpenAI error format.
func (p *Router) NotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "invalid_request_error", "not_found",
		"unknown endpoint: "+r.Method+" "+r.URL.Path)
}

// Disabled answers /v1 requests when the proxy is turned off. The status is
// 404 (as when the routes did not exist) with a clear message in the body.
func (p *Router) Disabled(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "invalid_request_error", "proxy_disabled",
		"the OpenAI-compatible proxy is disabled on this manager")
}

// ListModels serves GET /v1/models: only models currently alive on some
// machine, named by alias when set, else by real name.
func (p *Router) ListModels(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	fresh := time.Since(p.builtAt) < p.ttl
	entries := p.entries
	p.mu.RUnlock()
	if !fresh {
		p.refresh("__list__")
		p.mu.RLock()
		entries = p.entries
		p.mu.RUnlock()
	}

	data := make([]openAIModel, 0, len(entries))
	for _, e := range entries {
		data = append(data, openAIModel{ID: e.id, Object: "model", Created: e.created, OwnedBy: "llamamanager"})
	}
	sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Object string        `json:"object"`
		Data   []openAIModel `json:"data"`
	}{Object: "list", Data: data})
}

// HandleGeneration serves POST /v1/chat/completions, /v1/completions and
// /v1/embeddings: extract "model", resolve the machine running it, and
// reverse-proxy the exchange untouched (bodies pass through, including the
// model echo field).
func (p *Router) HandleGeneration(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "",
			"failed to read request body: "+err.Error())
		return
	}

	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "",
			"invalid JSON body: "+err.Error())
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "",
			"you must provide a model parameter")
		return
	}

	tgts, ok := p.resolve(req.Model)
	if !ok || len(tgts) == 0 {
		writeError(w, http.StatusNotFound, "invalid_request_error", "model_not_available",
			fmt.Sprintf("model '%s' is not running on any machine", req.Model))
		return
	}
	tgt := tgts[0]
	if tgt.warming {
		// The machine reports the model "starting": not a 404 (the model
		// exists and was just asked to start) — tell the client to retry.
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "server_error", "model_warming_up",
			fmt.Sprintf("model '%s' is starting on machine '%s'", req.Model, tgt.machine))
		return
	}

	p.forward(w, r, body, req.Model, tgt)
}

// forward proxies the exchange to tgt, retrying once on a stale target
// (upstream went away between routing and connecting — the model just stopped
// or crashed while the table still held it). The body was buffered, so a
// retry can replay it; a retry happens only when no response bytes reached
// the client yet.
func (p *Router) forward(w http.ResponseWriter, r *http.Request, body []byte, modelName string, tgt target) {
	for attempt := 0; attempt < 2; attempt++ {
		if attempt == 1 {
			p.invalidate(modelName)
			tgts, ok := p.resolve(modelName)
			if !ok || len(tgts) == 0 {
				writeError(w, http.StatusNotFound, "invalid_request_error", "model_not_available",
					fmt.Sprintf("model '%s' stopped and is not running on any machine anymore", modelName))
				return
			}
			tgt = tgts[0]
		}

		stale := &atomic.Bool{}
		rp := p.makeProxy(tgt, stale, attempt == 0)

		// Replay the buffered body for this attempt.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		rp.ServeHTTP(w, r)

		if !stale.Load() {
			return
		}
		p.logger("Proxy: stale target for model '%s' (%s), re-resolving", modelName, tgt.baseURL)
	}
}

// makeProxy builds a ReverseProxy for one attempt at tgt. When the upstream
// refuses/reset the connection before any response (stale), and a retry is
// still possible, the error handler stays silent so the outer loop can
// re-resolve and replay.
func (p *Router) makeProxy(tgt target, stale *atomic.Bool, retryPossible bool) *httputil.ReverseProxy {
	targetURL, err := url.Parse(tgt.baseURL)
	if err != nil {
		// Targets are built from validated config parts; this is unreachable.
		p.logger("Proxy: invalid target URL %q: %v", tgt.baseURL, err)
	}
	rp := httputil.NewSingleHostReverseProxy(targetURL)

	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host
		// The coordinator key must not leak to llama-server upstreams.
		req.Header.Del("Authorization")
		req.Header.Del("api-key")
	}

	rp.FlushInterval = -1 // flush after every write: required for SSE, harmless for JSON
	rp.Transport = &staleDetectTransport{base: p.upstream, stale: stale}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if stale.Load() && retryPossible {
			return // left unwritten so forward() can re-resolve and replay
		}
		p.logger("Proxy: upstream %s for %s failed: %v", tgt.baseURL, r.URL.Path, err)
		writeError(w, http.StatusBadGateway, "server_error", "api_error", "model backend unavailable")
	}
	return rp
}

// staleDetectTransport flags RoundTrip failures caused by an upstream that
// went away (connection refused/reset) — the stale-target signal. Failures
// after the response headers (mid-stream) are not flagged: those cannot be
// retried.
type staleDetectTransport struct {
	base  http.RoundTripper
	stale *atomic.Bool
}

func (t *staleDetectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil && isStaleError(err) {
		t.stale.Store(true)
	}
	return resp, err
}

func isStaleError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET)
}
