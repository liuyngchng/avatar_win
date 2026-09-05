//go:build windows

package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/jchv/go-webview2"
	"github.com/liuyngchng/avatar-desktop-x64/internal/brain"
)

type webviewRenderer struct {
	webview webview2.WebView
	events  chan brain.Event
	done    chan struct{}
	// srv serves the embedded web assets; closed via Shutdown in Close().
	srv *http.Server
	// closeOnce guards done so Close() and the window-destroy path can both
	// signal completion without double-closing the channel.
	closeOnce sync.Once
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
	slog.Info("renderer: serving", "url", url)

	r := &webviewRenderer{
		events: make(chan brain.Event, 16),
		done:   make(chan struct{}),
		srv:    srv,
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
				Title:      "Avatar Desktop",
				Width:      800,
				Height:     1000,
				Center:     false,
				Borderless: true,
			},
		})

		if w == nil {
			ready <- errors.New("webview2: failed to create window")
			return
		}

		// Position the window at the bottom-right of the screen from the
		// start, so it doesn't flash centered before the JS resizes it.
		w.BottomRight()

		// Make the WebView2 control background transparent so the
		// Windows desktop shows through behind the VRM avatar.
		if err := w.SetDefaultBackgroundColor(0, 0, 0, 0); err != nil {
			slog.Warn("renderer: transparent bg warning", "error", err)
		}

		r.webview = w

		// Bind goBridge_sendEvent so JS can send events to Go.
		if err := w.Bind("goBridge_sendEvent", func(jsonStr string) {
			var ev brain.Event
			if err := json.Unmarshal([]byte(jsonStr), &ev); err != nil {
				slog.Error("renderer: bad event from JS", "error", err)
				return
			}
			select {
			case r.events <- ev:
			default:
				slog.Warn("renderer: dropping event (channel full)", "type", ev.Type)
			}
		}); err != nil {
			slog.Warn("renderer: bind warning", "error", err)
		}

		// Bind goBridge_moveWindow so JS can drag the borderless window.
		if err := w.Bind("goBridge_moveWindow", func(dx, dy int) {
			w.MoveBy(dx, dy)
		}); err != nil {
			slog.Warn("renderer: bind moveWindow warning", "error", err)
		}

		// Bind goBridge_setWindowSize so JS can resize + bottom-right the window.
		// The avatar should be screen-height/2, so JS computes the desired
		// pixel size from the avatar's bounding box and the screen metrics,
		// then calls this to apply it.
		if err := w.Bind("goBridge_setWindowSize", func(width, height int) {
			w.SetSize(width, height, webview2.HintNone)
			w.BottomRight()
		}); err != nil {
			slog.Warn("renderer: bind setWindowSize warning", "error", err)
		}

		w.Navigate(url)
		ready <- nil

		// Run the Windows message pump. This blocks until Destroy() is
		// called (which posts WM_QUIT).
		slog.Info("renderer: entering message loop")
		w.Run()
		slog.Info("renderer: message loop exited")

		// The window was destroyed (user clicked X, or programmatic
		// Destroy()).  Signal that the renderer is done so the main
		// goroutine can exit cleanly.
		r.closeOnce.Do(func() { close(r.done) })
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
		slog.Error("renderer: marshal error", "error", err)
		return
	}
	js := "if(window.handleMessage)handleMessage(" + strconv.Quote(string(data)) + ")"
	// Eval must be called on the main UI thread (WebView2 requirement).
	// Dispatch marshals the call onto the thread that owns the message pump.
	r.webview.Dispatch(func() {
		r.webview.Eval(js)
	})
}

func (r *webviewRenderer) Events() <-chan brain.Event {
	return r.events
}

func (r *webviewRenderer) Done() <-chan struct{} {
	return r.done
}

func (r *webviewRenderer) Close() {
	// Shut down the embedded asset HTTP server so the listener is released
	// and in-flight requests drain cleanly instead of being abandoned.
	if r.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = r.srv.Shutdown(ctx)
		cancel()
	}
	r.webview.Destroy()
	// Signal completion — either the window-destroy path in the goroutine
	// below already closed `done`, or we close it here (Close() called
	// programmatically).  Whoever gets there first wins.
	r.closeOnce.Do(func() { close(r.done) })
}