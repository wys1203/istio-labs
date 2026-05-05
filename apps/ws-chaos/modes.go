package main

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

type ModeName string

const (
	ModeNormal        ModeName = "normal"
	ModeIdleHang      ModeName = "idle-hang"
	ModeSlowClose     ModeName = "slow-close"
	ModeRandomRST     ModeName = "random-rst"
	ModeDropAfter     ModeName = "drop-after"
	ModeCPUBurn       ModeName = "cpu-burn"
	ModeRejectUpgrade ModeName = "reject-upgrade"
)

type ModeParams struct {
	Ratio   float64 `json:"ratio,omitempty"`   // for random-rst (0..1)
	Seconds int     `json:"seconds,omitempty"` // for drop-after, slow-close
}

type Mode struct {
	Mode   ModeName   `json:"mode"`
	Params ModeParams `json:"params,omitempty"`
}

var validModes = map[ModeName]bool{
	ModeNormal: true, ModeIdleHang: true, ModeSlowClose: true,
	ModeRandomRST: true, ModeDropAfter: true, ModeCPUBurn: true,
	ModeRejectUpgrade: true,
}

func parseMode(body []byte) (Mode, error) {
	var m Mode
	if err := json.Unmarshal(body, &m); err != nil {
		return m, fmt.Errorf("parse mode body: %w", err)
	}
	if !validModes[m.Mode] {
		return m, fmt.Errorf("unknown mode %q", m.Mode)
	}
	if m.Mode == ModeRandomRST && (m.Params.Ratio < 0 || m.Params.Ratio > 1) {
		return m, fmt.Errorf("random-rst ratio must be in [0,1], got %v", m.Params.Ratio)
	}
	if (m.Mode == ModeDropAfter || m.Mode == ModeSlowClose) && m.Params.Seconds < 0 {
		return m, fmt.Errorf("seconds must be >= 0")
	}
	return m, nil
}

// currentMode is the live atomic pointer to the active Mode (immutable per pointer).
var currentMode atomic.Value // stores Mode

func init() {
	currentMode.Store(Mode{Mode: ModeNormal})
}

func getMode() Mode { return currentMode.Load().(Mode) }
func setMode(m Mode) { currentMode.Store(m) }
