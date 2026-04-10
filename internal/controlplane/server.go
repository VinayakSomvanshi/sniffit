package controlplane

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vinayak/sniffit/internal/ai"
	"github.com/vinayak/sniffit/internal/rules"
	"github.com/vinayak/sniffit/webhook"
)

const (
	dataDir      = "./sniffit-data"
	alertsFile   = "./sniffit-data/alerts.json"
	settingsFile = "./sniffit-data/settings.json"
	maxAlerts    = 2000
)

// Settings holds all user-configurable control plane parameters.
type Settings struct {
	AITriggerLevel  string `json:"ai_trigger_level"`
	SlackWebhookURL string `json:"slack_webhook_url"`
	MinNotifySev    string `json:"min_notify_severity"`
}

// ── Topology State ────────────────────────────────────────────────────────────

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

// ── Server ────────────────────────────────────────────────────────────────────

type Server struct {
	mu       sync.RWMutex
	alerts   []*rules.Alert
	clients  map[chan *rules.Alert]bool
	mutator  *webhook.Mutator
	aiAgent  *ai.Agent
	settings Settings

	topoMu   sync.RWMutex
	topology *TopologyState
}

func NewServer() *Server {
	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[server] Warning: could not create data dir %s: %v", dataDir, err)
	}

	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		webhookURL = "http://sniffit-control-plane.default.svc.cluster.local:8080"
	}

	s := &Server{
		alerts:  make([]*rules.Alert, 0, 100),
		clients: make(map[chan *rules.Alert]bool),
		mutator: webhook.NewMutator(webhookURL),
		aiAgent: ai.NewAgent(),
		settings: Settings{
			AITriggerLevel: "error",
			MinNotifySev:   "warn",
		},
		topology: &TopologyState{
			Queues:    make(map[string]*QueueInfo),
			Exchanges: make(map[string]*ExchangeInfo),
			Consumers: make(map[string]*ConsumerInfo),
		},
	}

	s.loadPersistedAlerts()
	s.loadPersistedSettings()
	return s
}

func (s *Server) Start(addr, certFile, keyFile string) error {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/alerts", s.handleAlerts)
	mux.HandleFunc("/api/alerts/history", s.handleGetAlerts)
	mux.HandleFunc("/api/events", s.handleEventsSSE)
	mux.HandleFunc("/api/analyze", s.handleAnalyze)
	mux.HandleFunc("/api/ai/health", s.handleAIHealth)
	mux.HandleFunc("/api/ai/stream", s.handleAIStreamAnalysis)
	mux.HandleFunc("/api/ai/chat", s.handleAIChat)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/topology", s.handleTopology)
	mux.HandleFunc("/api/topology/clear", s.handleTopologyClear)

	// Webhook
	mux.HandleFunc("/mutate", s.mutator.HandleMutate)

	// UI static files
	fs := http.FileServer(http.Dir("./ui/dist"))
	mux.Handle("/", fs)

	log.Printf("Control Plane listening on %s (TLS: %v)", addr, certFile != "")
	if certFile != "" && keyFile != "" {
		return http.ListenAndServeTLS(addr, certFile, keyFile, mux)
	}
	return http.ListenAndServe(addr, mux)
}

// ── Persistence ───────────────────────────────────────────────────────────────

func (s *Server) loadPersistedAlerts() {
	data, err := os.ReadFile(alertsFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[server] Could not read alerts file: %v", err)
		}
		return
	}
	var loaded []*rules.Alert
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[server] Could not parse persisted alerts: %v", err)
		return
	}
	s.alerts = loaded
	log.Printf("[server] Loaded %d persisted alerts from disk", len(loaded))

	// Rebuild topology from history
	for _, a := range loaded {
		s.updateTopologyFromAlert(a)
	}
}

func (s *Server) saveAlertsToDisk() {
	// Called while s.mu is held (read lock is fine, data won't change)
	data, err := json.MarshalIndent(s.alerts, "", "  ")
	if err != nil {
		log.Printf("[server] Could not marshal alerts: %v", err)
		return
	}
	tmp := alertsFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Printf("[server] Could not write alerts temp file: %v", err)
		return
	}
	if err := os.Rename(tmp, alertsFile); err != nil {
		log.Printf("[server] Could not rename alerts file: %v", err)
	}
}

func (s *Server) loadPersistedSettings() {
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[server] Could not read settings file: %v", err)
		}
		return
	}
	var loaded Settings
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[server] Could not parse persisted settings: %v", err)
		return
	}
	s.settings = loaded
	log.Printf("[server] Loaded persisted settings from disk: AI trigger=%s", s.settings.AITriggerLevel)
}

func (s *Server) saveSettingsToDisk() {
	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return
	}
	tmp := settingsFile + ".tmp"
	_ = os.WriteFile(tmp, data, 0644)
	_ = os.Rename(tmp, settingsFile)
}

// ── Topology Tracking ────────────────────────────────────────────────────────

func (s *Server) updateTopologyFromAlert(a *rules.Alert) {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()

	now := a.Timestamp
	if now.IsZero() {
		now = time.Now().UTC()
	}
	entity := a.Entity
	s.topology.LastUpdated = now

	switch a.RuleName {
	case "queue_declare":
		if entity == "" {
			break
		}
		// Parse durable/exclusive/auto-delete from plain_english
		q, exists := s.topology.Queues[entity]
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
		s.topology.Queues[entity] = q

	case "queue_deleted":
		if q, ok := s.topology.Queues[entity]; ok {
			q.Active = false
		}

	case "exchange_declare":
		if entity == "" {
			break
		}
		ex, exists := s.topology.Exchanges[entity]
		if !exists {
			ex = &ExchangeInfo{Name: entity, FirstSeen: now, Active: true}
		}
		// Parse type from plain_english: "type=direct"
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
		s.topology.Exchanges[entity] = ex

	case "exchange_deleted":
		if ex, ok := s.topology.Exchanges[entity]; ok {
			ex.Active = false
		}

	case "consumer_registered":
		// entity is the queue name; extract consumer tag from plain_english
		tag := extractBetween(a.PlainEnglish, "tag=", ",")
		if tag == "" {
			tag = fmt.Sprintf("consumer@%s", entity)
		}
		tag = strings.Trim(tag, `"`)
		exclusive := strings.Contains(a.PlainEnglish, "(exclusive)")
		s.topology.Consumers[tag] = &ConsumerInfo{
			ConsumerTag: tag,
			Queue:       entity,
			Exclusive:   exclusive,
			FirstSeen:   now,
			Active:      true,
		}

	case "consumer_cancelled":
		// entity is the consumer tag
		if c, ok := s.topology.Consumers[entity]; ok {
			c.Active = false
		}

	case "connection_opened":
		s.topology.Connections = append(s.topology.Connections, &ConnectionInfo{
			Source:   filepath.Base(a.MethodName),
			OpenedAt: now,
			Active:   true,
		})
		// Keep last 50 connections
		if len(s.topology.Connections) > 50 {
			s.topology.Connections = s.topology.Connections[len(s.topology.Connections)-50:]
		}

	case "connection_closed_cleanly", "connection_closed_unexpectedly", "connection_close_unparseable":
		// Mark the most recent active connection as closed
		t := now
		for i := len(s.topology.Connections) - 1; i >= 0; i-- {
			if s.topology.Connections[i].Active {
				s.topology.Connections[i].Active = false
				s.topology.Connections[i].ClosedAt = &t
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

// ── HTTP Handlers ────────────────────────────────────────────────────────────

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	switch r.Method {
	case http.MethodPost:
		s.handlePostAlert(w, r)
	case http.MethodDelete:
		s.mu.Lock()
		s.alerts = make([]*rules.Alert, 0)
		s.saveAlertsToDisk()
		s.mu.Unlock()
		// Also clear topology
		s.topoMu.Lock()
		s.topology = &TopologyState{
			Queues:    make(map[string]*QueueInfo),
			Exchanges: make(map[string]*ExchangeInfo),
			Consumers: make(map[string]*ConsumerInfo),
		}
		s.topoMu.Unlock()
		log.Printf("[server] Alert history and topology cleared")
		w.WriteHeader(http.StatusOK)
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
	if alert.Timestamp.IsZero() {
		alert.Timestamp = time.Now().UTC()
	}

	// Update topology derived from this event
	s.updateTopologyFromAlert(&alert)

	s.mu.Lock()
	alertPtr := &alert
	if len(s.alerts) >= maxAlerts {
		s.alerts = s.alerts[1:]
	}
	s.alerts = append(s.alerts, alertPtr)
	// Persist to disk asynchronously
	go s.saveAlertsToDisk()

	// Broadcast to SSE clients
	for client := range s.clients {
		select {
		case client <- alertPtr:
		default:
			close(client)
			delete(s.clients, client)
		}
	}
	s.mu.Unlock()

	// AI background diagnosis
	s.mu.RLock()
	triggerLevel := s.settings.AITriggerLevel
	s.mu.RUnlock()
	if triggerLevel == "" {
		triggerLevel = "error"
	}

	shouldTrigger := false
	switch triggerLevel {
	case "all":
		shouldTrigger = true
	case "info":
		shouldTrigger = true
	case "warn":
		shouldTrigger = alert.Severity == "warn" || alert.Severity == "error"
	case "error":
		shouldTrigger = alert.Severity == "error"
	}

	if shouldTrigger {
		go func(ptr *rules.Alert) {
			diagnosis, err := s.aiAgent.Diagnose(ptr)
			if err != nil {
				log.Printf("[server] Background AI %s failed: %v", ptr.RuleID, err)
				return
			}
			s.mu.Lock()
			ptr.AIDiagnosis = diagnosis
			// Save updated alert to disk
			go s.saveAlertsToDisk()
			for client := range s.clients {
				select {
				case client <- ptr:
				default:
					close(client)
					delete(s.clients, client)
				}
			}
			s.mu.Unlock()
		}(alertPtr)
	}

	log.Printf("[server] Received alert [%s] %s from %s", alert.Severity, alert.RuleName, alert.MethodName)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(s.alerts)
}

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

	clientChan := make(chan *rules.Alert, 100)
	s.mu.Lock()
	s.clients[clientChan] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, clientChan)
		s.mu.Unlock()
		close(clientChan)
	}()

	// Initial ping to confirm connection is alive
	fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
	flusher.Flush()

	// Keepalive ticker to prevent proxy timeouts
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

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	s.topoMu.RLock()
	defer s.topoMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(s.topology)
}

func (s *Server) handleTopologyClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.topoMu.Lock()
	s.topology = &TopologyState{
		Queues:    make(map[string]*QueueInfo),
		Exchanges: make(map[string]*ExchangeInfo),
		Consumers: make(map[string]*ConsumerInfo),
	}
	s.topoMu.Unlock()
	w.WriteHeader(http.StatusOK)
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"diagnosis": diagnosis})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodGet {
		s.mu.RLock()
		defer s.mu.RUnlock()
		json.NewEncoder(w).Encode(s.settings)
		return
	}

	if r.Method == http.MethodPost {
		var updated Settings
		if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		// Selectively update only provided non-empty fields
		if updated.AITriggerLevel != "" {
			s.settings.AITriggerLevel = updated.AITriggerLevel
		}
		if updated.SlackWebhookURL != "" {
			s.settings.SlackWebhookURL = updated.SlackWebhookURL
		}
		if updated.MinNotifySev != "" {
			s.settings.MinNotifySev = updated.MinNotifySev
		}
		// Persist
		go s.saveSettingsToDisk()
		log.Printf("[server] Settings updated: %+v", s.settings)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// ── AI Capabilities Handlers ────────────────────────────────────────────────

func (s *Server) handleAIHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	alertsCopy := append([]*rules.Alert(nil), s.alerts...)
	s.mu.RUnlock()

	summary, err := s.aiAgent.GenerateHealthSummary(alertsCopy)
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
	s.mu.RLock()
	alertsCopy := append([]*rules.Alert(nil), s.alerts...)
	s.mu.RUnlock()

	analysis, err := s.aiAgent.AnalyzeStream(alertsCopy)
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

	s.mu.RLock()
	alertsCopy := append([]*rules.Alert(nil), s.alerts...)
	s.mu.RUnlock()

	reply, err := s.aiAgent.Chat(req.Message, req.History, alertsCopy)
	if err != nil {
		log.Printf("[server] AI chat error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"reply": reply})
}
