package deployramp

import (
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	batchIntervalMS = 5000
	batchMaxSize    = 20
	maxReconnDelay  = 30 * time.Second
)

// flagCache stores flags in memory and manages the WebSocket connection
// for real-time updates and evaluation batching.
type flagCache struct {
	mu    sync.RWMutex
	flags map[string]FlagData

	wsMu           sync.Mutex
	ws             *websocket.Conn
	wsURL          string
	reconnectDelay time.Duration
	closed         bool

	evalMu     sync.Mutex
	evalBatch  []EvaluationEvent
	batchTimer *time.Timer
	stopCh     chan struct{}
}

func newFlagCache() *flagCache {
	return &flagCache{
		flags:          make(map[string]FlagData),
		reconnectDelay: time.Second,
		stopCh:         make(chan struct{}),
	}
}

// setFlags replaces all cached flags.
func (fc *flagCache) setFlags(flags []FlagData) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.flags = make(map[string]FlagData, len(flags))
	for _, f := range flags {
		fc.flags[f.Name] = f
	}
}

// getFlag returns a flag by name, or nil if not found.
func (fc *flagCache) getFlag(name string) *FlagData {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	f, ok := fc.flags[name]
	if !ok {
		return nil
	}
	return &f
}

// queueEvaluation adds an evaluation event to the batch.
// Flushes immediately if the batch reaches max size.
func (fc *flagCache) queueEvaluation(event EvaluationEvent) {
	fc.evalMu.Lock()
	defer fc.evalMu.Unlock()

	fc.evalBatch = append(fc.evalBatch, event)
	if len(fc.evalBatch) >= batchMaxSize {
		fc.flushEvaluationsLocked()
	} else if fc.batchTimer == nil {
		fc.batchTimer = time.AfterFunc(batchIntervalMS*time.Millisecond, func() {
			fc.evalMu.Lock()
			defer fc.evalMu.Unlock()
			fc.flushEvaluationsLocked()
		})
	}
}

// flushEvaluationsLocked sends the current batch over WebSocket.
// Caller must hold evalMu.
func (fc *flagCache) flushEvaluationsLocked() {
	if fc.batchTimer != nil {
		fc.batchTimer.Stop()
		fc.batchTimer = nil
	}
	if len(fc.evalBatch) == 0 {
		return
	}

	batch := fc.evalBatch
	fc.evalBatch = nil

	fc.sendMessage(wsMessage{
		Type:        "evaluation_batch",
		Evaluations: batch,
	})
}

// sendMessage sends a JSON message over the WebSocket if connected.
func (fc *flagCache) sendMessage(msg wsMessage) {
	fc.wsMu.Lock()
	defer fc.wsMu.Unlock()
	if fc.ws == nil {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_ = fc.ws.WriteMessage(websocket.TextMessage, data)
}

// connectWebSocket initiates the WebSocket connection for real-time flag updates.
func (fc *flagCache) connectWebSocket(rawURL string) {
	fc.wsMu.Lock()
	fc.wsURL = rawURL
	fc.closed = false
	fc.wsMu.Unlock()
	go fc.openConnection()
}

func (fc *flagCache) openConnection() {
	fc.wsMu.Lock()
	if fc.closed || fc.wsURL == "" {
		fc.wsMu.Unlock()
		return
	}
	wsURL := fc.wsURL
	fc.wsMu.Unlock()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fc.scheduleReconnect()
		return
	}

	fc.wsMu.Lock()
	fc.ws = conn
	fc.reconnectDelay = time.Second
	fc.wsMu.Unlock()

	// Read loop
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			fc.wsMu.Lock()
			fc.ws = nil
			fc.wsMu.Unlock()
			fc.scheduleReconnect()
			return
		}

		var msg wsMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		if msg.Type == "flag_updated" || msg.Type == "flags_refreshed" {
			if msg.Flags != nil {
				fc.setFlags(msg.Flags)
			}
		}
	}
}

func (fc *flagCache) scheduleReconnect() {
	fc.wsMu.Lock()
	defer fc.wsMu.Unlock()
	if fc.closed {
		return
	}

	delay := fc.reconnectDelay
	fc.reconnectDelay *= 2
	if fc.reconnectDelay > maxReconnDelay {
		fc.reconnectDelay = maxReconnDelay
	}

	time.AfterFunc(delay, func() {
		fc.openConnection()
	})
}

// close shuts down the cache, flushes pending evaluations, and disconnects.
func (fc *flagCache) close() {
	fc.wsMu.Lock()
	fc.closed = true
	fc.wsMu.Unlock()

	// Flush remaining evaluations
	fc.evalMu.Lock()
	fc.flushEvaluationsLocked()
	fc.evalMu.Unlock()

	fc.wsMu.Lock()
	defer fc.wsMu.Unlock()

	if fc.ws != nil {
		_ = fc.ws.Close()
		fc.ws = nil
	}

	fc.mu.Lock()
	fc.flags = make(map[string]FlagData)
	fc.mu.Unlock()
}

// buildWSURL constructs the WebSocket URL from the base HTTP URL and token.
func buildWSURL(baseURL, token string) string {
	wsProto := "wss"
	if strings.HasPrefix(baseURL, "http://") {
		wsProto = "ws"
	}
	host := strings.TrimPrefix(baseURL, "https://")
	host = strings.TrimPrefix(host, "http://")
	return wsProto + "://" + host + "/ws?token=" + url.QueryEscape(token)
}

// init logger prefix for the package
func init() {
	log.SetFlags(log.LstdFlags)
}
