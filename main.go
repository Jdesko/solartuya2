package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	Host           = "https://openapi.tuyaeu.com"
	UpdateInterval = 2 * time.Second
	TokenExpiry    = 4 * time.Hour
)

var (
	ClientID       = os.Getenv("TUYA_CLIENT_ID")
	Secret         = os.Getenv("TUYA_SECRET")
	Token          string
	tokenExpiry    time.Time
	lastEnergyData EnergyData
	dataMutex      sync.RWMutex
	clients        map[chan string]bool
	clientsMutex   sync.Mutex
)

type DeviceStatus struct {
	Code  string      `json:"code"`
	Value interface{} `json:"value"`
}

type DeviceResponse struct {
	Result  []DeviceStatus `json:"result"`
	Success bool           `json:"success"`
	T       int64          `json:"t"`
}

type EnergyData struct {
	PainelSolar    float64 `json:"painel_solar"`
	ImportacaoRede float64 `json:"importacao_rede"`
	CarregadorEV   float64 `json:"carregador_ev"`
	ConsumoCasa    float64 `json:"consumo_casa"`
	VoltagemMedia  float64 `json:"voltagem_media"`
	Timestamp      int64   `json:"timestamp"`
}

var devices = map[string]map[string]string{
	"bfe2a8f17ebd1d39d1hctm": {
		"cur_power2":   "importacao_rede",
		"cur_voltage2": "voltagem",
	},
	"bfcaa7d812363ec7eeimjs": {
		"cur_power1":   "carregador_ev",
		"cur_power2":   "painel_solar",
		"cur_voltage1": "voltagem",
	},
}

func main() {
	clients = make(map[chan string]bool)

	// Initialize token
	if err := refreshToken(); err != nil {
		log.Fatal("Initial token refresh failed:", err)
	}

	// Start background services
	go dataUpdater()
	go tokenRefresher()

	// Configure server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Configure routes with CORS support
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/api/energy", energyHandler)
	http.HandleFunc("/api/stream", sseHandler)
	http.HandleFunc("/api/refresh", refreshHandler)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	log.Printf("Server starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	enableCORS(&w)
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "./static/index.html")
}

func energyHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(&w)
	dataMutex.RLock()
	defer dataMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lastEnergyData)
}

func refreshHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(&w)
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	newData, err := fetchEnergyData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	dataMutex.Lock()
	lastEnergyData = *newData
	dataMutex.Unlock()

	broadcastUpdate(newData)
	w.WriteHeader(http.StatusOK)
}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(&w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	messageChan := make(chan string)
	clientsMutex.Lock()
	clients[messageChan] = true
	clientsMutex.Unlock()

	defer func() {
		clientsMutex.Lock()
		delete(clients, messageChan)
		close(messageChan)
		clientsMutex.Unlock()
	}()

	// Send initial data
	dataMutex.RLock()
	initialData, _ := json.Marshal(lastEnergyData)
	dataMutex.RUnlock()
	fmt.Fprintf(w, "data: %s\n\n", initialData)
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case msg := <-messageChan:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-notify:
			return
		}
	}
}

func enableCORS(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
	(*w).Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	(*w).Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// ... [Keep all other existing functions exactly as they were]
// (dataUpdater, fetchEnergyData, getDeviceStatus, refreshToken,
// tokenRefresher, buildHeader, buildSign, sha256Hash, hmacSha256,
// getUrlStr, getHeaderStr, convertToFloat, broadcastUpdate)
