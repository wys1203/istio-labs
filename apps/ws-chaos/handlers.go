package main

import (
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func handleGetMode(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(getMode())
}

func handleSetMode(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m, err := parseMode(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setMode(m)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	mode := getMode()

	if mode.Mode == ModeRejectUpgrade {
		closeTotal.WithLabelValues("reject").Inc()
		http.Error(w, "upgrade rejected by chaos mode", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	activeConnections.Inc()
	defer activeConnections.Dec()

	switch mode.Mode {
	case ModeIdleHang:
		// Hold the connection open; never read or write. Eventually peer or LB tears it down.
		<-r.Context().Done()
		closeTotal.WithLabelValues("timeout").Inc()
		_ = conn.Close()
		return
	case ModeRandomRST:
		if rand.Float64() < mode.Params.Ratio {
			abortConnection(conn)
			closeTotal.WithLabelValues("rst").Inc()
			return
		}
	case ModeDropAfter:
		go func() {
			time.Sleep(time.Duration(mode.Params.Seconds) * time.Second)
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "drop-after"))
			_ = conn.Close()
		}()
	case ModeCPUBurn:
		go burnCPU(r.Context())
	}

	// Echo loop with heartbeat.
	conn.SetPongHandler(func(string) error { return nil })
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			t, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			messagesTotal.WithLabelValues("rx").Inc()
			if mode.Mode == ModeSlowClose {
				time.Sleep(time.Duration(mode.Params.Seconds) * time.Second)
				_ = conn.Close()
				return
			}
			if err := conn.WriteMessage(t, msg); err != nil {
				return
			}
			messagesTotal.WithLabelValues("tx").Inc()
		}
	}()

	for {
		select {
		case <-done:
			closeTotal.WithLabelValues("normal").Inc()
			return
		case <-heartbeat.C:
			_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
		}
	}
}

// abortConnection forces a TCP RST by setting linger to 0 before close.
func abortConnection(conn *websocket.Conn) {
	if tcp, ok := conn.NetConn().(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.NetConn().Close()
}

func burnCPU(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// busy loop
			for i := 0; i < 1_000_000; i++ {
				_ = i * i
			}
		}
	}
}
