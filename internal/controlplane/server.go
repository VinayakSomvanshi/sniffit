package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vinayak/sniffit/internal/ai"
	"github.com/vinayak/sniffit/internal/auth"
	"github.com/vinayak/sniffit/internal/rules"
	"github.com/vinayak/sniffit/internal/store"
	"github.com/vinayak/sniffit/webhook"
)


// Settings holds all user-configurable control plane parameters.
type Settings struct {
	AITriggerLevel  string `json:"ai_trigger_level"`
	SlackWebhookURL string `json:"slack_webhook_url"`
	MinNotifySev    string `json:"min_notify_severity"`
}

// ── Topology State (in-memory cache backed by store) ─────────────────────────

type QueueInfo struct {
	Name       string    `json:"name"`
	Durable    bool      `json:"durable"`
	Exclusive  bool      `json:"exclusive"`
	AutoDelete bool      `json:"auto_delete"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Active     bool      `json:"active"`
}

type ExchangeInfo struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Durable   bool      `json:"durable"`
	FirstSeen time.Time `json:"first_seen"`
	Active    bool      `json:"active"`
}

type ConsumerInfo struct {
	ConsumerTag string    `json:"consumer_tag"`
	Queue       string    `json:"queue"`
	Exclusive   bool      `json:"exclusive"`
	FirstSeen   time.Time `json:"first_seen"`
	Active      bool      `json:"active"`
}

type ConnectionInfo struct {
	Source   string     `json:"source"`
	OpenedAt time.Time  `json:"opened_at"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
	Active   bool       `json:"active"`
}

type TopologyState struct {
	Queues      map[string]*QueueInfo    `json:"queues"`
	Exchanges   map[string]*ExchangeInfo `json:"exchanges"`
	Consumers   map[string]*ConsumerInfo `json:"consumers"`
	Connections []*ConnectionInfo        `json:"connections"`
	LastUpdated time.Time                `json:"last_updated"`
}

// ── Server ─────────────────────────────────────────────────────────────────

type Server struct {
	addr   string
	store  *store.Store
	mutator *webhook.Mutator
	aiAgent *ai.Agent
	settings Settings

	// SSE clients, keyed by channel, with tenant info
	clientsMu sync.RWMutex
	clients   map[chan *rules.Alert]*tenantClient

	// Topology cache (per-tenant)
	topoMu   sync.RWMutex
	topology map[string]*TopologyState // tenantID -> topology

	rateLimiter *auth.SlidingWindowLimiter
}

type tenantClient struct {
	tenantID string
}

func NewServer(addr string, store *store.Store) *Server {
	webhookURL := os.Getenv("CONTROL_PLANE_URL")
	if webhookURL == "" {
		webhookURL = "https://sniffit.sniffit.svc.cluster.local:8080"
	}

	insecureSkip := (os.Getenv("INSECURE_SKIP_VERIFY") == "true")

	s := &Server{
		addr:   addr,
		store:  store,
		mutator: webhook.NewMutator(webhookURL, insecureSkip),
		aiAgent: ai.NewAgent(),
		settings: Settings{
			AITriggerLevel: "error",
			MinNotifySev:   "warn",
		},
		clients:   make(map[chan *rules.Alert]*tenantClient),
		topology:  make(map[string]*TopologyState),
		rateLimiter: auth.NewSlidingWindowLimiter(),
	}

	// Load default tenant settings
	if st, err := store.GetSettings(context.Background(), "default"); err == nil {
		s.settings = Settings{
			AITriggerLevel:  st.AITriggerLevel,
			SlackWebhookURL: st.SlackWebhookURL,
			MinNotifySev:    st.MinNotifySev,
		}
	}

	return s
}

func (s *Server) Start(certFile, keyFile string) error {
	mux := http.NewServeMux()

	// Apply auth middleware + audit logger to all /api/* routes
	apiChain := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, s.withMiddleware(handler))
	}

	// Auth-only endpoints (no audit)
	mux.HandleFunc("/auth/login", s.handleLogin)

	// API endpoints
	apiChain("/api/alerts", s.handleAlerts)
	apiChain("/api/alerts/history", s.handleGetAlerts)
	apiChain("/api/events", s.handleEventsSSE)
	apiChain("/api/analyze", s.handleAnalyze)
	apiChain("/api/ai/health", s.handleAIHealth)
	apiChain("/api/ai/stream", s.handleAIStreamAnalysis)
	apiChain("/api/ai/chat", s.handleAIChat)
	apiChain("/api/settings", s.handleSettings)
	apiChain("/api/topology", s.handleTopology)
	apiChain("/api/topology/clear", s.handleTopologyClear)

	// Admin-only endpoints
	adminChain := func(pattern string, handler http.HandlerFunc) {
		// AuthMiddleware wraps handler, then AdminOnly wraps the result
		wrapped := auth.AuthMiddleware(s.store, s.rateLimiter)(handler)
		wrapped = auth.AdminOnly(wrapped)
		wrapped = auth.AuditLogger(s.store, s.rateLimiter)(wrapped)
		mux.Handle(pattern, wrapped)
	}
	adminChain("/api/keys", s.handleKeys)
	adminChain("/api/keys/", s.handleKeysByID)
	adminChain("/api/tenants", s.handleTenants)
	adminChain("/api/audit-log", s.handleAuditLog)

	// Webhook (no auth)
	mux.HandleFunc("/mutate", s.mutator.HandleMutate)

	// UI static files
	fs := http.FileServer(http.Dir("./ui/dist"))
	mux.Handle("/", fs)

	log.Printf("Control Plane listening on %s (TLS: %v)", s.addr, certFile != "")
	if certFile != "" && keyFile != "" {
		return http.ListenAndServeTLS(s.addr, certFile, keyFile, mux)
	}
	return http.ListenAndServe(s.addr, mux)
}

// withMiddleware applies auth + audit middleware and returns an http.Handler.
func (s *Server) withMiddleware(handler http.HandlerFunc) http.Handler {
	wrapped := auth.AuthMiddleware(s.store, s.rateLimiter)(handler)
	wrapped = auth.AuditLogger(s.store, s.rateLimiter)(wrapped)
	return wrapped
}

// tenantFromCtx extracts the tenant ID from the request context.
func (s *Server) tenantFromCtx(r *http.Request) string {
	tc, ok := auth.FromTenant(r.Context())
	if !ok {
		return "default"
	}
	return tc.TenantID
}

// ── Auth ──────────────────────────────────────────────────────────────────

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
		return
	}

	// Validate the provided key
	tc, err := auth.NewAPIKeyValidator(s.store).ValidateBearer(r.Context(),
		"Bearer "+req.APIKey)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_api_key"})
		return
	}

	auth.WriteJSON(w, http.StatusOK, map[string]string{
		"token":     req.APIKey,
		"tenant_id": tc.TenantID,
		"is_admin":  strconv.FormatBool(tc.IsAdmin),
	})
}

// ── Alerts ─────────────────────────────────────────────────────────────────

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	switch r.Method {
	case http.MethodPost:
		s.handlePostAlert(w, r)
	case http.MethodDelete:
		s.handleDeleteAlerts(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePostAlert(w http.ResponseWriter, r *http.Request) {
	var alert rules.Alert
	if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	tenantID := s.tenantFromCtx(r)
	if alert.TenantID == "" {
		alert.TenantID = tenantID
	} else if alert.TenantID != tenantID {
		// Probes can only send alerts for their own tenant
		http.Error(w, `{"error":"tenant_mismatch"}`, http.StatusForbidden)
		return
	}

	if alert.Timestamp.IsZero() {
		alert.Timestamp = time.Now().UTC()
	}

	// Persist to DB
	id, err := s.store.InsertAlert(r.Context(), &alert)
	if err != nil {
		log.Printf("[server] insert alert failed: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	alert.ID = id

	// Update topology cache
	s.updateTopologyFromAlert(&alert)

	// Broadcast to SSE clients in the same tenant
	s.broadcastAlert(&alert)

	// AI background diagnosis
	s.triggerAI(r.Context(), &alert)

	log.Printf("[server] [%s] %s %s from %s", tenantID, alert.Severity, alert.RuleName, alert.MethodName)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDeleteAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID := s.tenantFromCtx(r)
	if err := s.store.DeleteAllAlerts(r.Context(), tenantID); err != nil {
		log.Printf("[server] delete alerts failed: %v", err)
	}
	// Clear topology cache
	s.topoMu.Lock()
	delete(s.topology, tenantID)
	s.topoMu.Unlock()
	log.Printf("[server] Alerts and topology cleared for tenant %s", tenantID)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID := s.tenantFromCtx(r)
	limit := 500
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	alerts, err := s.store.ListAlerts(r.Context(), tenantID, limit, offset)
	if err != nil {
		log.Printf("[server] list alerts failed: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(alerts)
}

// broadcastAlert sends an alert to all SSE clients in the same tenant.
func (s *Server) broadcastAlert(alert *rules.Alert) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	for ch, tc := range s.clients {
		if tc.tenantID != alert.TenantID {
			continue
		}
		select {
		case ch <- alert:
		default:
			// Slow client — drop and remove
			close(ch)
			delete(s.clients, ch)
		}
	}
}

// ── SSE ───────────────────────────────────────────────────────────────────

func (s *Server) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	tenantID := s.tenantFromCtx(r)
	clientChan := make(chan *rules.Alert, 100)

	s.clientsMu.Lock()
	s.clients[clientChan] = &tenantClient{tenantID: tenantID}
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, clientChan)
		s.clientsMu.Unlock()
		close(clientChan)
	}()

	// Initial ping
	fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case <-ticker.C:
			fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		case alert, ok := <-clientChan:
			if !ok {
				return
			}
			bytes, _ := json.Marshal(alert)
			fmt.Fprintf(w, "data: %s\n\n", string(bytes))
			flusher.Flush()
		}
	}
}

// ── Topology ────────────────────────────────────────────────────────────────

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	tenantID := s.tenantFromCtx(r)
	s.topoMu.RLock()
	topo, ok := s.topology[tenantID]
	s.topoMu.RUnlock()
	if !ok {
		topo = &TopologyState{
			Queues:    make(map[string]*QueueInfo),
			Exchanges: make(map[string]*ExchangeInfo),
			Consumers: make(map[string]*ConsumerInfo),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(topo)
}

func (s *Server) handleTopologyClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenantID := s.tenantFromCtx(r)
	s.topoMu.Lock()
	delete(s.topology, tenantID)
	s.topoMu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) updateTopologyFromAlert(a *rules.Alert) {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()

	tenantID := a.TenantID
	topo, ok := s.topology[tenantID]
	if !ok {
		topo = &TopologyState{
			Queues:    make(map[string]*QueueInfo),
			Exchanges: make(map[string]*ExchangeInfo),
			Consumers: make(map[string]*ConsumerInfo),
		}
		s.topology[tenantID] = topo
	}

	now := a.Timestamp
	if now.IsZero() {
		now = time.Now().UTC()
	}
	entity := a.Entity
	topo.LastUpdated = now

	switch a.RuleName {
	case "queue_declare":
		if entity == "" {
			return
		}
		q, exists := topo.Queues[entity]
		if !exists {
			q = &QueueInfo{Name: entity, FirstSeen: now, Active: true}
		}
		q.LastSeen = now
		q.Active = true
		if strings.Contains(a.PlainEnglish, "durable=true") {
			q.Durable = true
		}
		if strings.Contains(a.PlainEnglish, "exclusive=true") {
			q.Exclusive = true
		}
		if strings.Contains(a.PlainEnglish, "auto-delete=true") {
			q.AutoDelete = true
		}
		topo.Queues[entity] = q

	case "queue_deleted":
		if q, ok := topo.Queues[entity]; ok {
			q.Active = false
		}

	case "exchange_declare":
		if entity == "" {
			return
		}
		ex, exists := topo.Exchanges[entity]
		if !exists {
			ex = &ExchangeInfo{Name: entity, FirstSeen: now, Active: true}
		}
		if idx := strings.Index(a.PlainEnglish, "type="); idx >= 0 {
			rest := a.PlainEnglish[idx+5:]
			end := strings.IndexAny(rest, " ,.")
			if end > 0 {
				ex.Type = rest[:end]
			}
		}
		if strings.Contains(a.PlainEnglish, "durable=true") {
			ex.Durable = true
		}
		ex.Active = true
		topo.Exchanges[entity] = ex

	case "exchange_deleted":
		if ex, ok := topo.Exchanges[entity]; ok {
			ex.Active = false
		}

	case "consumer_registered":
		tag := extractBetween(a.PlainEnglish, "tag=", ",")
		if tag == "" {
			tag = fmt.Sprintf("consumer@%s", entity)
		}
		tag = strings.Trim(tag, `"`)
		exclusive := strings.Contains(a.PlainEnglish, "(exclusive)")
		topo.Consumers[tag] = &ConsumerInfo{
			ConsumerTag: tag,
			Queue:       entity,
			Exclusive:   exclusive,
			FirstSeen:   now,
			Active:      true,
		}

	case "consumer_cancelled":
		if c, ok := topo.Consumers[entity]; ok {
			c.Active = false
		}

	case "connection_opened":
		topo.Connections = append(topo.Connections, &ConnectionInfo{
			Source:   filepath.Base(a.MethodName),
			OpenedAt: now,
			Active:   true,
		})
		if len(topo.Connections) > 50 {
			topo.Connections = topo.Connections[len(topo.Connections)-50:]
		}

	case "connection_closed_cleanly", "connection_closed_unexpectedly", "connection_close_unparseable":
		t := now
		for i := len(topo.Connections) - 1; i >= 0; i-- {
			if topo.Connections[i].Active {
				topo.Connections[i].Active = false
				topo.Connections[i].ClosedAt = &t
				break
			}
		}
	}
}

func extractBetween(s, after, before string) string {
	i := strings.Index(s, after)
	if i < 0 {
		return ""
	}
	s = s[i+len(after):]
	j := strings.Index(s, before)
	if j < 0 {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:j])
}

// ── AI ─────────────────────────────────────────────────────────────────────

func (s *Server) triggerAI(ctx context.Context, alert *rules.Alert) {
	st, err := s.store.GetSettings(ctx, alert.TenantID)
	if err != nil {
		return
	}
	triggerLevel := st.AITriggerLevel
	if triggerLevel == "" {
		triggerLevel = "error"
	}

	shouldTrigger := false
	switch triggerLevel {
	case "all", "info":
		shouldTrigger = true
	case "warn":
		shouldTrigger = alert.Severity == "warn" || alert.Severity == "error"
	case "error":
		shouldTrigger = alert.Severity == "error"
	}

	if !shouldTrigger {
		return
	}

	go func() {
		diagnosis, err := s.aiAgent.Diagnose(alert)
		if err != nil {
			log.Printf("[server] AI diagnosis failed for %s: %v", alert.RuleID, err)
			return
		}
		if err := s.store.UpdateAlertDiagnosis(context.Background(), alert.TenantID, alert.ID, diagnosis); err != nil {
			log.Printf("[server] update diagnosis failed: %v", err)
			return
		}
		alert.AIDiagnosis = diagnosis
		s.broadcastAlert(alert)
	}()
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var alert rules.Alert
	if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	diagnosis, err := s.aiAgent.Diagnose(&alert)
	if err != nil {
		log.Printf("[server] AI diagnosis error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]string{"diagnosis": diagnosis})
}

func (s *Server) handleAIHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenantID := s.tenantFromCtx(r)
	alerts, err := s.store.ListAlerts(r.Context(), tenantID, 500, 0)
	if err != nil {
		log.Printf("[server] AI health error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	summary, err := s.aiAgent.GenerateHealthSummary(alerts)
	if err != nil {
		log.Printf("[server] AI health error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(summary)
}

func (s *Server) handleAIStreamAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tenantID := s.tenantFromCtx(r)
	alerts, err := s.store.ListAlerts(r.Context(), tenantID, 500, 0)
	if err != nil {
		log.Printf("[server] AI stream analysis error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	analysis, err := s.aiAgent.AnalyzeStream(alerts)
	if err != nil {
		log.Printf("[server] AI stream analysis error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(analysis)
}

type chatRequest struct {
	Message string           `json:"message"`
	History []ai.ChatMessage `json:"history"`
}

func (s *Server) handleAIChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	tenantID := s.tenantFromCtx(r)
	alerts, err := s.store.ListAlerts(r.Context(), tenantID, 500, 0)
	if err != nil {
		log.Printf("[server] AI chat error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	reply, err := s.aiAgent.Chat(req.Message, req.History, alerts)
	if err != nil {
		log.Printf("[server] AI chat error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	auth.WriteJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

// ── Settings ────────────────────────────────────────────────────────────────

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodGet {
		st, err := s.store.GetSettings(r.Context(), s.tenantFromCtx(r))
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(st)
		return
	}

	if r.Method == http.MethodPost {
		var updated Settings
		if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		tenantID := s.tenantFromCtx(r)
		st := &store.Settings{
			TenantID:       tenantID,
			AITriggerLevel: updated.AITriggerLevel,
			SlackWebhookURL: updated.SlackWebhookURL,
			MinNotifySev:   updated.MinNotifySev,
		}
		if err := s.store.UpsertSettings(r.Context(), st); err != nil {
			log.Printf("[server] upsert settings failed: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		if tenantID == "default" {
			s.settings = updated
		}
		log.Printf("[server] Settings updated for tenant %s", tenantID)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// ── Keys ───────────────────────────────────────────────────────────────────

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	tenantID := s.tenantFromCtx(r)
	switch r.Method {
	case http.MethodGet:
		keys, err := s.store.ListAPIKeys(r.Context(), tenantID)
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		auth.WriteJSON(w, http.StatusOK, keys)

	case http.MethodPost:
		var req struct {
			Name      string `json:"name"`
			TenantID  string `json:"tenant_id"`
			RateLimit int    `json:"rate_limit"`
			IsAdmin   bool   `json:"is_admin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		keyTenant := req.TenantID
		if keyTenant == "" {
			keyTenant = tenantID
		}
		rateLimit := req.RateLimit
		if rateLimit == 0 {
			rateLimit = 100
		}
		apiKey, plaintext, err := s.store.GenerateAPIKey(r.Context(), req.Name, keyTenant, rateLimit, req.IsAdmin)
		if err != nil {
			log.Printf("[server] generate key failed: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		// Return plaintext only once
		auth.WriteJSON(w, http.StatusCreated, map[string]any{
			"id":         apiKey.ID,
			"name":       apiKey.Name,
			"tenant_id":  apiKey.TenantID,
			"rate_limit": apiKey.RateLimit,
			"is_admin":   apiKey.IsAdmin,
			"key":        plaintext, // only returned on creation
		})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleKeysByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/keys/")
	if id == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		// Admin can list any key; non-admin only sees their tenant's
		keys, err := s.store.ListAPIKeys(r.Context(), s.tenantFromCtx(r))
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		for _, k := range keys {
			if k.ID == id {
				auth.WriteJSON(w, http.StatusOK, k)
				return
			}
		}
		http.Error(w, "Not found", http.StatusNotFound)
	case http.MethodDelete:
		if err := s.store.DeleteAPIKey(r.Context(), id); err != nil {
			log.Printf("[server] delete key failed: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// ── Tenants ─────────────────────────────────────────────────────────────────

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tenants, err := s.store.ListTenants(r.Context())
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		auth.WriteJSON(w, http.StatusOK, tenants)
	case http.MethodPost:
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		if req.ID == "" || req.Name == "" {
			http.Error(w, `{"error":"id and name required"}`, http.StatusBadRequest)
			return
		}
		tenant, err := s.store.CreateTenant(r.Context(), req.ID, req.Name)
		if err != nil {
			log.Printf("[server] create tenant failed: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		auth.WriteJSON(w, http.StatusCreated, tenant)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// ── Audit Log ───────────────────────────────────────────────────────────────

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := s.store.ListAuditEntries(r.Context(), s.tenantFromCtx(r), limit, offset)
	if err != nil {
		log.Printf("[server] list audit entries failed: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(entries)
}
