package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"llamamanager/config"
	"llamamanager/fleet"
	"llamamanager/models"

	"github.com/gorilla/mux"
)

// stubLocal is a controllable LocalSource. currents is consumed one
// GetCurrentServer call at a time (routing-table rebuilds), falling back to
// last afterwards — this is what lets tests change the world between the two
// attempts of a stale-target retry.
type stubLocal struct {
	mu       sync.Mutex
	models   map[string]*models.ModelConfig
	currents []*models.RunningServer
	last     *models.RunningServer
}

func (s *stubLocal) ListModels() map[string]*models.ModelConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.models
}

func (s *stubLocal) GetCurrentServer() *models.RunningServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.currents) > 0 {
		c := s.currents[0]
		s.currents = s.currents[1:]
		return c
	}
	return s.last
}

func newTestProxy(t *testing.T, local *stubLocal, workers []config.WorkerConfig, apiKey string, authEnabled bool) (http.Handler, *Router) {
	t.Helper()
	flt := fleet.NewRegistry(workers, nil)
	p := New(local, flt, apiKey, authEnabled, nil)
	return proxyTestRouter(p), p
}

// proxyTestRouter mirrors the /v1 wiring in server.setupRouter.
func proxyTestRouter(p *Router) http.Handler {
	r := mux.NewRouter()
	v1 := r.PathPrefix("/v1").Subrouter()
	v1.Use(p.RequireAuth)
	v1.HandleFunc("/models", p.ListModels).Methods("GET")
	v1.HandleFunc("/chat/completions", p.HandleGeneration).Methods("POST")
	v1.HandleFunc("/completions", p.HandleGeneration).Methods("POST")
	v1.HandleFunc("/embeddings", p.HandleGeneration).Methods("POST")
	v1.NotFoundHandler = http.HandlerFunc(p.NotFound)
	return r
}

func llamaPort(t *testing.T, handler http.Handler) int {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func runningServer(name string, port int) *models.RunningServer {
	cfg := models.ModelConfig{Name: name}
	if port > 0 {
		p := port
		cfg.Port = &p
	}
	return &models.RunningServer{
		ModelConfig: cfg,
		Status:      models.StatusRunning,
		StartTime:   time.Now(),
	}
}

func chatBody(model string) string {
	return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model)
}

const testKey = "coordkey123456"

func TestV1ModelsAliveOnly(t *testing.T) {
	local := &stubLocal{
		currents: []*models.RunningServer{runningServer("m1", 9001)},
	}
	// m1 runs under alias gpt-x: published as gpt-x, addressable as both.
	local.currents[0].ModelConfig.Alias = "gpt-x"
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.APIResponse{Success: true, Data: models.ModelListResponse{
			Models: []models.ModelItem{
				{ModelConfig: models.ModelConfig{Name: "wmodel", Port: intPtr(8099)}, Active: true},
			},
		}})
	}))
	defer fakeWorker.Close()

	router, _ := newTestProxy(t, local, []config.WorkerConfig{{URL: fakeWorker.URL}}, testKey, true)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}

	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if payload.Object != "list" {
		t.Fatalf("object = %q", payload.Object)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("data = %+v, want exactly the two alive models", payload.Data)
	}
	if payload.Data[0].ID != "gpt-x" || payload.Data[1].ID != "wmodel" {
		t.Fatalf("ids = %+v, want [gpt-x wmodel] (alias published, sorted)", payload.Data)
	}
	for _, m := range payload.Data {
		if m.OwnedBy != "llamamanager" || m.Object != "model" {
			t.Fatalf("entry = %+v", m)
		}
	}
	if strings.Contains(rr.Body.String(), "m1") {
		t.Fatal("a model with an alias must be published under the alias only")
	}
}

func TestChatRoutesToLocalAndStripsAuth(t *testing.T) {
	var gotAuth string
	var gotPath string
	port := llamaPort(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cmpl-1","model":"m1"}`)
	}))

	local := &stubLocal{
		currents: []*models.RunningServer{runningServer("m1", port)},
		last:     runningServer("m1", port),
	}
	router, _ := newTestProxy(t, local, nil, testKey, true)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody("m1")))
	req.Header.Set("Authorization", "Bearer "+testKey)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cmpl-1") {
		t.Fatalf("body = %s, want the upstream response passed through", rr.Body.String())
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("coordinator credentials leaked upstream: %q", gotAuth)
	}
}

func TestAliasAddressing(t *testing.T) {
	port := llamaPort(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cmpl-2"}`)
	}))
	rs := runningServer("m1", port)
	rs.ModelConfig.Alias = "gpt-x"
	local := &stubLocal{
		currents: []*models.RunningServer{rs},
		last:     rs,
	}
	router, _ := newTestProxy(t, local, nil, "", false)

	// Address by alias and by real name — both must work.
	for _, model := range []string{"gpt-x", "m1"} {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody(model)))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("model %q: status = %d, body %s", model, rr.Code, rr.Body.String())
		}
	}
}

func TestWarmingReturns503(t *testing.T) {
	rs := runningServer("m1", 9001)
	rs.Status = models.StatusStarting
	local := &stubLocal{currents: []*models.RunningServer{rs}, last: rs}
	router, _ := newTestProxy(t, local, nil, "", false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody("m1")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rr.Code, rr.Body.String())
	}
	if ra := rr.Header().Get("Retry-After"); ra != "5" {
		t.Fatalf("Retry-After = %q, want 5", ra)
	}
	if !strings.Contains(rr.Body.String(), "model_warming_up") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestUnknownModel404(t *testing.T) {
	local := &stubLocal{}
	router, _ := newTestProxy(t, local, nil, "", false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody("nope")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "model_not_available") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestMalformedBodyAndMissingModel400(t *testing.T) {
	local := &stubLocal{}
	router, _ := newTestProxy(t, local, nil, "", false)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"malformed json", `{"model":`, "invalid JSON"},
		{"missing model", `{"messages":[]}`, "must provide a model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
			if rr.Code != 400 {
				t.Fatalf("status = %d, want 400 (body %s)", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("body = %s, want it to contain %q", rr.Body.String(), tc.want)
			}
		})
	}
}

func TestStaleTargetRetriesAndRecovers(t *testing.T) {
	// First target: a live server that then dies (connection refused).
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadPort, err := strconv.Atoi(url2Port(dead.URL))
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()

	// Second target: comes up under a different port; the stub serves it on
	// the second GetCurrentServer call (the post-invalidation rebuild).
	liveHits := 0
	var mu sync.Mutex
	livePort := llamaPort(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		liveHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cmpl-live"}`)
	}))

	local := &stubLocal{
		currents: []*models.RunningServer{
			runningServer("m1", deadPort),
			runningServer("m1", livePort),
		},
		last: runningServer("m1", livePort),
	}
	router, _ := newTestProxy(t, local, nil, "", false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody("m1")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body %s — the retry should have recovered on the live upstream", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cmpl-live") {
		t.Fatalf("body = %s", rr.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if liveHits != 1 {
		t.Fatalf("live upstream hits = %d, want 1", liveHits)
	}
}

func TestStaleTargetExhaustedReturns502(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadPort, err := strconv.Atoi(url2Port(dead.URL))
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()

	local := &stubLocal{
		currents: []*models.RunningServer{runningServer("m1", deadPort)},
		last:     runningServer("m1", deadPort),
	}
	router, _ := newTestProxy(t, local, nil, "", false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody("m1")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "api_error") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestChatRoutesToWorkerLlamaPort(t *testing.T) {
	var gotAuth atomic.Value
	llamaHits := 0
	var mu sync.Mutex
	llamaPort := llamaPort(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		llamaHits++
		mu.Unlock()
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cmpl-w"}`)
	}))

	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeWorkerModels(t, w,
			models.ModelItem{ModelConfig: models.ModelConfig{Name: "wm", Port: intPtr(llamaPort)}, Active: true})
	}))
	defer fakeWorker.Close()

	local := &stubLocal{}
	router, _ := newTestProxy(t, local, []config.WorkerConfig{{URL: fakeWorker.URL}}, "", false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody("wm")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if llamaHits != 1 {
		t.Fatalf("llama hits = %d, want 1", llamaHits)
	}
	if v := gotAuth.Load(); v != nil && v.(string) != "" {
		t.Fatalf("credentials leaked to worker llama-server: %q", v)
	}
}

func TestStreamingPassthrough(t *testing.T) {
	llamaPort := llamaPort(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 20; i++ {
			fmt.Fprintf(w, "data: {\"i\":%d}\n\n", i)
			flusher.Flush()
			time.Sleep(40 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))

	local := &stubLocal{
		currents: []*models.RunningServer{runningServer("m1", llamaPort)},
		last:     runningServer("m1", llamaPort),
	}
	router, _ := newTestProxy(t, local, nil, "", false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody("m1")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// httptest.ResponseRecorder cannot show incremental delivery; use a real
	// client against a real listener for the timing assertions.
	ts := httptest.NewServer(router)
	defer ts.Close()

	start := time.Now()
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(chatBody("m1")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var firstData time.Time
	chunks := 0
	doneSeen := false
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if firstData.IsZero() {
			firstData = time.Now()
		}
		chunks++
		if line == "data: [DONE]" {
			doneSeen = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !doneSeen {
		t.Fatal("data: [DONE] not seen")
	}
	if chunks != 21 {
		t.Fatalf("chunks = %d, want 20 data chunks + [DONE]", chunks)
	}

	firstLatency := firstData.Sub(start)
	total := time.Since(start)
	if firstLatency > 200*time.Millisecond {
		t.Fatalf("first chunk after %v — the proxy is buffering instead of streaming", firstLatency)
	}
	if total < 400*time.Millisecond {
		t.Fatalf("total stream time %v — upstream chunks should take ~800ms; stream was truncated", total)
	}
}

func TestClientCancelPropagates(t *testing.T) {
	var canceled atomic.Bool
	llamaPort := llamaPort(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		for {
			select {
			case <-r.Context().Done():
				canceled.Store(true)
				return
			default:
			}
			fmt.Fprint(w, "data: {\"i\":1}\n\n")
			flusher.Flush()
			time.Sleep(30 * time.Millisecond)
		}
	}))

	local := &stubLocal{
		currents: []*models.RunningServer{runningServer("m1", llamaPort)},
		last:     runningServer("m1", llamaPort),
	}
	router, _ := newTestProxy(t, local, nil, "", false)
	ts := httptest.NewServer(router)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/v1/chat/completions", strings.NewReader(chatBody("m1")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}

	// Read one chunk, then abandon the request.
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if canceled.Load() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("upstream never observed the client cancellation")
}

func TestV1AuthModes(t *testing.T) {
	local := &stubLocal{}
	router, _ := newTestProxy(t, local, nil, testKey, true)

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"no auth", nil, 401},
		{"wrong bearer", map[string]string{"Authorization": "Bearer wrongkey"}, 401},
		{"correct bearer", map[string]string{"Authorization": "Bearer " + testKey}, 200},
		{"api-key header", map[string]string{"api-key": testKey}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/models", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, tc.want, rr.Body.String())
			}
			if tc.want == 401 && !strings.Contains(rr.Body.String(), "invalid_api_key") {
				t.Fatalf("401 body = %s, want OpenAI-format invalid_api_key error", rr.Body.String())
			}
		})
	}

	// Auth disabled: no key configured, everything passes.
	openRouter, _ := newTestProxy(t, &stubLocal{}, nil, "", false)
	rr := httptest.NewRecorder()
	openRouter.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/models", nil))
	if rr.Code != 200 {
		t.Fatalf("auth-disabled status = %d", rr.Code)
	}
}

func TestUnknownV1EndpointOpenAI404(t *testing.T) {
	local := &stubLocal{}
	router, _ := newTestProxy(t, local, nil, "", false)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/nope", nil))

	if rr.Code != 404 {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not_found") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

// TestAliasCollisionWarns covers the routing-table-build warning: an alias on
// one machine colliding with a real model name on another.
func TestAliasCollisionWarns(t *testing.T) {
	fakeWorker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeWorkerModels(t, w,
			models.ModelItem{ModelConfig: models.ModelConfig{Name: "shared", Port: intPtr(8099)}, Active: true})
	}))
	defer fakeWorker.Close()

	local := &stubLocal{
		currents: []*models.RunningServer{runningServer("m1", 9001)},
		last:     runningServer("m1", 9001),
	}
	// m1 is aliased to "shared", colliding with the worker's real model name.
	// The alias must sit on the running server's config — that is what the
	// rebuild reads.
	local.currents[0].ModelConfig.Alias = "shared"
	local.last.ModelConfig.Alias = "shared"

	router, p := newTestProxy(t, local, []config.WorkerConfig{{URL: fakeWorker.URL}}, "", false)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/models", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}

	p.mu.RLock()
	warned := len(p.warned)
	p.mu.RUnlock()
	if warned == 0 {
		t.Fatal("alias colliding with a real model name should warn")
	}
}

func writeWorkerModels(t *testing.T, w http.ResponseWriter, items ...models.ModelItem) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(models.APIResponse{Success: true, Data: models.ModelListResponse{Models: items}}); err != nil {
		t.Fatalf("fake worker write: %v", err)
	}
}

func url2Port(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	return u.Port()
}

func intPtr(i int) *int { return &i }
