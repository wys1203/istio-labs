package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	activeConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "wschaos_active_connections",
		Help: "Number of currently open WebSocket connections.",
	})
	messagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wschaos_messages_total",
		Help: "Messages received/sent by ws-chaos.",
	}, []string{"direction"})
	closeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wschaos_close_total",
		Help: "WebSocket close events by reason.",
	}, []string{"reason"})
)
