package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	desktopStateVersion = 1
	minWindowWidth      = 560
	minWindowHeight     = 480
	defaultWindowWidth  = 1200
	defaultWindowHeight = 800
	baseDPI             = 96
)

type desktopState struct {
	Version   int  `json:"version"`
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
	LastPort  int  `json:"last_port"`
}

func defaultDesktopState() desktopState {
	return desktopState{
		Version: desktopStateVersion,
		X:       120,
		Y:       80,
		Width:   defaultWindowWidth,
		Height:  defaultWindowHeight,
	}
}

// scaleForDPI converts a 96-DPI logical size to physical pixels.
func scaleForDPI(value, dpi int) int {
	if dpi <= 0 {
		dpi = baseDPI
	}
	return value * dpi / baseDPI
}

// defaultWindowRect returns the first-launch window relative to the monitor
// work area: the default logical size scaled to dpi, shrunk to fit the work
// area and centered in it.
func defaultWindowRect(dpi, workWidth, workHeight int) (x, y, width, height int) {
	width = min(scaleForDPI(defaultWindowWidth, dpi), workWidth)
	height = min(scaleForDPI(defaultWindowHeight, dpi), workHeight)
	return (workWidth - width) / 2, (workHeight - height) / 2, width, height
}

func validDesktopState(state desktopState) bool {
	return state.Version == desktopStateVersion &&
		state.Width >= minWindowWidth &&
		state.Width <= 32768 &&
		state.Height >= minWindowHeight &&
		state.Height <= 32768 &&
		state.X >= -1<<30 && state.X <= 1<<30 &&
		state.Y >= -1<<30 && state.Y <= 1<<30 &&
		state.LastPort >= 0 &&
		state.LastPort <= 65535
}

func loadDesktopState(path string) (desktopState, error) {
	state := defaultDesktopState()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read desktop state: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return state, errors.New("desktop state cannot be read or is too large")
	}
	state = desktopState{}
	if err := json.Unmarshal(raw, &state); err != nil {
		return defaultDesktopState(), fmt.Errorf("parse desktop state: %w", err)
	}
	if !validDesktopState(state) {
		return defaultDesktopState(), errors.New("desktop state is invalid or too small")
	}
	return state, nil
}

func saveDesktopState(path string, state desktopState) error {
	if !validDesktopState(state) {
		return errors.New("desktop state is invalid")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".desktop-state-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(temp)
	}
	if _, err := file.Write(raw); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
