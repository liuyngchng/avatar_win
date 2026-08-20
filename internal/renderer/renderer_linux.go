//go:build linux

package renderer

import (
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/liuyngchng/avatar-pc/internal/brain"
	"github.com/zserge/lorca"
)

type lorcaRenderer struct {
	ui     lorca.UI
	events chan brain.Event
}

// newPlatformRenderer creates a Linux renderer using Lorca
// (system Chrome in --app mode).
func newPlatformRenderer(webFS fs.FS) (Renderer, error) {
	// Serve the embedded web assets on a random local port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port

	srv := &http.Server{Handler: http.FileServer(http.FS(webFS))}
	go srv.Serve(listener)

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/index.html"
	log.Printf("renderer: serving at %s", url)

	// Lorca spawns Chrome in --app mode (frameless window).
	// --no-sandbox is needed for snap Chromium in WSL/containers.
	// --remote-allow-origins=* is required by Chrome 111+ for the DevTools
	// WebSocket handshake that Lorca uses to talk to the page.
	// The WebGL-related flags force software rendering (SwiftShader/ANGLE),
	// which is required under WSLg where a real GPU context is unavailable.
	ui, err := lorca.New(url, "", 1280, 800,
		"--disable-sync", "--no-first-run", "--no-sandbox",
		"--remote-allow-origins=*",
		"--enable-unsafe-swiftshader",
		"--use-gl=angle", "--use-angle=swiftshader",
		"--enable-webgl", "--ignore-gpu-blocklist",
		"--disable-gpu")
	if err != nil {
		listener.Close()
		return nil, err
	}

	r := &lorcaRenderer{
		ui:     ui,
		events: make(chan brain.Event, 16),
	}

	// Expose a bridge function callable from JS:
	//   window.goBridge.sendEvent('{"type":"tap"}')
	if err := ui.Bind("goBridge_sendEvent", r.handleJSEvent); err != nil {
		log.Printf("renderer: bind warning: %v", err)
	}

	return r, nil
}

func (r *lorcaRenderer) handleJSEvent(jsonStr string) {
	var ev brain.Event
	if err := json.Unmarshal([]byte(jsonStr), &ev); err != nil {
		log.Printf("renderer: bad event from JS: %v", err)
		return
	}
	select {
	case r.events <- ev:
	default:
		log.Printf("renderer: dropping event (channel full): %s", ev.Type)
	}
}

func (r *lorcaRenderer) SendMessage(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("renderer: marshal error: %v", err)
		return
	}
	js := "if(window.handleMessage)handleMessage(" + strconv.Quote(string(data)) + ")"
	r.ui.Eval(js)
}

func (r *lorcaRenderer) Events() <-chan brain.Event {
	return r.events
}

func (r *lorcaRenderer) Close() {
	r.ui.Close()
}