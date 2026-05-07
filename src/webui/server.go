package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/admin"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

type WebUIServer struct {
	server         *http.Server
	log            core.Logger
	listen         string
	password       string
	sessions       map[string]time.Time
	sessionsMux    sync.RWMutex
	failedAttempts map[string]*FailedLoginInfo
	attemptsMux    sync.RWMutex
	admin          *admin.AdminSocket
}

type LoginRequest struct {
	Password string `json:"password"`
}

type FailedLoginInfo struct {
	Count        int
	LastAttempt  time.Time
	BlockedUntil time.Time
}

const (
	MaxFailedAttempts = 3
	BlockDuration     = 1 * time.Minute
	AttemptWindow     = 15 * time.Minute
)

func Server(listen string, password string, log core.Logger) *WebUIServer {
	return &WebUIServer{
		listen:         listen,
		password:       password,
		log:            log,
		sessions:       make(map[string]time.Time),
		failedAttempts: make(map[string]*FailedLoginInfo),
	}
}

func (w *WebUIServer) SetAdmin(admin *admin.AdminSocket) {
	w.admin = admin
}

func (w *WebUIServer) generateSessionID() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(bytes)
}

func (w *WebUIServer) isValidSession(sessionID string) bool {
	w.sessionsMux.RLock()
	defer w.sessionsMux.RUnlock()

	expiry, exists := w.sessions[sessionID]
	if !exists {
		return false
	}

	if time.Now().After(expiry) {
		go func() {
			w.sessionsMux.Lock()
			delete(w.sessions, sessionID)
			w.sessionsMux.Unlock()
		}()
		return false
	}

	return true
}

func (w *WebUIServer) createSession() string {
	sessionID := w.generateSessionID()
	expiry := time.Now().Add(24 * time.Hour)

	w.sessionsMux.Lock()
	w.sessions[sessionID] = expiry
	w.sessionsMux.Unlock()

	return sessionID
}

func (w *WebUIServer) getClientIP(r *http.Request) string {
	// Check X-Forwarded-For first (for reverse proxies)
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		ips := strings.Split(forwarded, ",")
		return strings.TrimSpace(ips[0])
	}

	realIP := r.Header.Get("X-Real-IP")
	if realIP != "" {
		return realIP
	}

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

func (w *WebUIServer) isIPBlocked(ip string) bool {
	w.attemptsMux.RLock()
	defer w.attemptsMux.RUnlock()

	info, exists := w.failedAttempts[ip]
	if !exists {
		return false
	}

	return time.Now().Before(info.BlockedUntil)
}

func (w *WebUIServer) recordFailedAttempt(ip string) {
	w.attemptsMux.Lock()
	defer w.attemptsMux.Unlock()

	now := time.Now()
	info, exists := w.failedAttempts[ip]

	if !exists {
		info = &FailedLoginInfo{}
		w.failedAttempts[ip] = info
	}

	// Reset sliding window if idle long enough
	if now.Sub(info.LastAttempt) > AttemptWindow {
		info.Count = 0
	}

	info.Count++
	info.LastAttempt = now

	if info.Count >= MaxFailedAttempts {
		info.BlockedUntil = now.Add(BlockDuration)
		w.log.Warnf("IP %s blocked for %v after %d failed login attempts", ip, BlockDuration, info.Count)
	}
}

func (w *WebUIServer) clearFailedAttempts(ip string) {
	w.attemptsMux.Lock()
	defer w.attemptsMux.Unlock()

	delete(w.failedAttempts, ip)
}

func (w *WebUIServer) cleanupFailedAttempts() {
	w.attemptsMux.Lock()
	defer w.attemptsMux.Unlock()

	now := time.Now()
	for ip, info := range w.failedAttempts {
		if now.After(info.BlockedUntil) && now.Sub(info.LastAttempt) > AttemptWindow {
			delete(w.failedAttempts, ip)
		}
	}
}

func (w *WebUIServer) cleanupExpiredSessions() {
	w.sessionsMux.Lock()
	defer w.sessionsMux.Unlock()

	now := time.Now()
	for sessionID, expiry := range w.sessions {
		if now.After(expiry) {
			delete(w.sessions, sessionID)
		}
	}
}

func (w *WebUIServer) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if w.password == "" {
			if r.URL.Path == "/login.html" {
				http.Redirect(rw, r, "/", http.StatusSeeOther)
				return
			}
			next(rw, r)
			return
		}

		cookie, err := r.Cookie("ygg_session")
		if err != nil || !w.isValidSession(cookie.Value) {
			if r.URL.Path == "/login.html" || strings.HasPrefix(r.URL.Path, "/auth/") {
				next(rw, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				rw.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(rw, r, "/login.html", http.StatusSeeOther)
			return
		}

		next(rw, r)
	}
}

func (w *WebUIServer) loginHandler(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := w.getClientIP(r)

	if w.isIPBlocked(clientIP) {
		w.log.Warnf("Blocked login attempt from %s (IP is temporarily blocked)", clientIP)
		http.Error(rw, "Too many failed attempts. Please try again later.", http.StatusTooManyRequests)
		return
	}

	var loginReq LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&loginReq); err != nil {
		http.Error(rw, "Invalid request", http.StatusBadRequest)
		return
	}

	if subtle.ConstantTimeCompare([]byte(loginReq.Password), []byte(w.password)) != 1 {
		w.log.Debugf("Authentication failed for request from %s", clientIP)
		w.recordFailedAttempt(clientIP)
		http.Error(rw, "Invalid password", http.StatusUnauthorized)
		return
	}

	w.clearFailedAttempts(clientIP)
	sessionID := w.createSession()
	http.SetCookie(rw, &http.Cookie{
		Name:     "ygg_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil, // Only set Secure flag if using HTTPS
		SameSite: http.SameSiteStrictMode,
		MaxAge:   24 * 60 * 60, // 24 hours
	})

	w.log.Infof("Successful authentication for IP %s", clientIP)
	rw.WriteHeader(http.StatusOK)
}

func (w *WebUIServer) logoutHandler(rw http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("ygg_session")
	if err == nil {
		w.sessionsMux.Lock()
		delete(w.sessions, cookie.Value)
		w.sessionsMux.Unlock()
	}

	http.SetCookie(rw, &http.Cookie{
		Name:     "ygg_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1, // Delete cookie
	})

	http.Redirect(rw, r, "/login.html", http.StatusSeeOther)
}

func (w *WebUIServer) adminAPIHandler(rw http.ResponseWriter, r *http.Request) {
	if w.admin == nil {
		http.Error(rw, "Admin API not available", http.StatusServiceUnavailable)
		return
	}

	// /api/admin/getSelf -> getSelf
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/")
	command := strings.Split(path, "/")[0]

	if command == "" {
		commands := w.admin.GetAvailableCommands()
		rw.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(rw).Encode(map[string]interface{}{
			"status":   "success",
			"commands": commands,
		}); err != nil {
			http.Error(rw, "Failed to encode response", http.StatusInternalServerError)
		}
		return
	}

	var args map[string]interface{}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			args = make(map[string]interface{})
		}
	} else {
		args = make(map[string]interface{})
	}

	result, err := w.callAdminHandler(command, args)
	if err != nil {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusBadRequest)
		if encErr := json.NewEncoder(rw).Encode(map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		}); encErr != nil {
			http.Error(rw, "Failed to encode error response", http.StatusInternalServerError)
		}
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(rw).Encode(map[string]interface{}{
		"status":   "success",
		"response": result,
	}); err != nil {
		http.Error(rw, "Failed to encode response", http.StatusInternalServerError)
	}
}

func (w *WebUIServer) callAdminHandler(command string, args map[string]interface{}) (interface{}, error) {
	argsBytes, err := json.Marshal(args)
	if err != nil {
		argsBytes = []byte("{}")
	}

	return w.admin.CallHandler(command, argsBytes)
}

type ConfigResponse struct {
	ConfigPath   string `json:"config_path"`
	ConfigFormat string `json:"config_format"`
	ConfigJSON   string `json:"config_json"`
}

type ConfigSetRequest struct {
	ConfigJSON string `json:"config_json"`
	ConfigPath string `json:"config_path,omitempty"`
	Format     string `json:"format,omitempty"`
	Restart    bool   `json:"restart,omitempty"`
}

type ConfigSetResponse struct {
	Success         bool   `json:"success"`
	Message         string `json:"message"`
	ConfigPath      string `json:"config_path"`
	RestartRequired bool   `json:"restart_required"`
}

func (w *WebUIServer) getConfigHandler(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	configInfo, err := config.GetCurrentConfig()
	if err != nil {
		w.log.Errorf("Failed to get current config: %v", err)
		http.Error(rw, "Failed to get configuration", http.StatusInternalServerError)
		return
	}

	configBytes, err := json.MarshalIndent(configInfo.Data, "", "  ")
	if err != nil {
		w.log.Errorf("Failed to marshal config to JSON: %v", err)
		http.Error(rw, "Failed to format configuration", http.StatusInternalServerError)
		return
	}

	response := ConfigResponse{
		ConfigPath:   configInfo.Path,
		ConfigFormat: configInfo.Format,
		ConfigJSON:   string(configBytes),
	}

	rw.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(rw).Encode(response); err != nil {
		w.log.Errorf("Failed to encode config response: %v", err)
		http.Error(rw, "Failed to encode response", http.StatusInternalServerError)
	}
}

func (w *WebUIServer) setConfigHandler(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, "Invalid request body", http.StatusBadRequest)
		return
	}

	var configData interface{}
	if err := json.Unmarshal([]byte(req.ConfigJSON), &configData); err != nil {
		response := ConfigSetResponse{
			Success: false,
			Message: fmt.Sprintf("Invalid JSON configuration: %v", err),
		}
		w.writeJSONResponse(rw, response)
		return
	}

	err := config.SaveConfig(configData, req.ConfigPath, req.Format)
	if err != nil {
		response := ConfigSetResponse{
			Success: false,
			Message: err.Error(),
		}
		w.writeJSONResponse(rw, response)
		return
	}

	configInfo, err := config.GetCurrentConfig()
	var configPath string = req.ConfigPath
	if err == nil && configInfo != nil {
		configPath = configInfo.Path
	}

	response := ConfigSetResponse{
		Success:         true,
		Message:         "Configuration saved successfully",
		ConfigPath:      configPath,
		RestartRequired: req.Restart,
	}

	if req.Restart {
		w.log.Infof("Configuration saved with restart request")
		go w.restartServer()
	}

	w.writeJSONResponse(rw, response)
}

func (w *WebUIServer) writeJSONResponse(rw http.ResponseWriter, data interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(rw).Encode(data); err != nil {
		w.log.Errorf("Failed to encode JSON response: %v", err)
		http.Error(rw, "Failed to encode response", http.StatusInternalServerError)
	}
}

func (w *WebUIServer) Start() error {
	if w.listen != "" {
		if _, _, err := net.SplitHostPort(w.listen); err != nil {
			return fmt.Errorf("invalid listen address: %v", err)
		}
	}

	go func() {
		sessionTicker := time.NewTicker(1 * time.Hour)
		attemptsTicker := time.NewTicker(5 * time.Minute)
		defer sessionTicker.Stop()
		defer attemptsTicker.Stop()

		for {
			select {
			case <-sessionTicker.C:
				w.cleanupExpiredSessions()
			case <-attemptsTicker.C:
				w.cleanupFailedAttempts()
			}
		}
	}()

	mux := http.NewServeMux()

	// Authentication endpoints — no auth middleware
	mux.HandleFunc("/auth/login", w.loginHandler)
	mux.HandleFunc("/auth/logout", w.logoutHandler)

	mux.HandleFunc("/api/admin/", w.authMiddleware(w.adminAPIHandler))
	mux.HandleFunc("/api/config/get", w.authMiddleware(w.getConfigHandler))
	mux.HandleFunc("/api/config/set", w.authMiddleware(w.setConfigHandler))

	// Static handler implementation varies by build tag (debug vs production)
	setupStaticHandler(mux, w)

	mux.HandleFunc("/", w.authMiddleware(func(rw http.ResponseWriter, r *http.Request) {
		serveFile(rw, r, w.log)
	}))

	// Health check — no auth required
	mux.HandleFunc("/health", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("OK"))
	})

	w.server = &http.Server{
		Addr:           w.listen,
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	w.log.Infof("WebUI server starting on %s", w.listen)

	if err := w.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("WebUI server failed: %v", err)
	}

	return nil
}

func (w *WebUIServer) Stop() error {
	if w.server != nil {
		return w.server.Close()
	}
	return nil
}

func (w *WebUIServer) restartServer() {
	w.log.Infof("Initiating server restart after configuration change")

	time.Sleep(1 * time.Second) // let the HTTP response flush before restarting

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		w.log.Errorf("Failed to find current process: %v", err)
		return
	}

	if err := sendRestartSignal(proc); err != nil {
		w.log.Errorf("Failed to send restart signal: %v", err)
		w.log.Infof("Please restart Yggdrasil manually to apply configuration changes")
	}
}
