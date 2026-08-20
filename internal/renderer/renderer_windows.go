//go:build windows

package renderer

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"

	"github.com/jchv/go-webview2"
	"github.com/liuyngchng/avatar-pc/internal/brain"
)

type webviewRenderer struct {
	webview webview2.WebView
	events  chan brain.Event
}

// newPlatformRenderer creates a Windows renderer using WebView2.
// The window is created on a dedicated OS thread (via runtime.LockOSThread)
// and the Windows message pump (w.Run()) runs on that same thread.
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

	r := &webviewRenderer{
		events: make(chan brain.Event, 16),
	}

	// Create the window and run the message pump on a dedicated OS thread.
	// Windows requires the message loop to run on the same thread that
	// created the window.  Without the message pump the window shows as
	// "Not Responding" and never paints.
	ready := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		w := webview2.NewWithOptions(webview2.WebViewOptions{
			Debug:     false,
			AutoFocus: true,
			WindowOptions: webview2.WindowOptions{
				Title:      "Avatar PC",
				Width:      800,
				Height:     1000,
				Center:     true,
				Borderless: true,
			},
		})

		if w == nil {
			ready <- errors.New("webview2: failed to create window")
			return
		}

		// Make the WebView2 control background transparent so the
		// Windows desktop shows through behind the VRM avatar.
		if err := w.SetDefaultBackgroundColor(0, 0, 0, 0); err != nil {
			log.Printf("renderer: transparent bg warning: %v", err)
		}

		r.webview = w

		// Bind goBridge_sendEvent so JS can send events to Go.
		if err := w.Bind("goBridge_sendEvent", func(jsonStr string) {
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
		}); err != nil {
			log.Printf("renderer: bind warning: %v", err)
		}

		w.Navigate(url)
		ready <- nil

		// Run the Windows message pump. This blocks until Destroy() is
		// called (which posts WM_QUIT).
		log.Println("renderer: entering message loop")
		w.Run()
		log.Println("renderer: message loop exited")
	}()

	if err := <-ready; err != nil {
		listener.Close()
		return nil, err
	}

	return r, nil
}

func (r *webviewRenderer) SendMessage(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("renderer: marshal error: %v", err)
		return
	}
	js := "if(window.handleMessage)handleMessage(" + strconv.Quote(string(data)) + ")"
	r.webview.Eval(js)
}

func (r *webviewRenderer) Events() <-chan brain.Event {
	return r.events
}

func (r *webviewRenderer) Close() {
	r.webview.Destroy()
}