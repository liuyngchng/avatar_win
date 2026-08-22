package renderer

import (
	"embed"
	"io/fs"

	"github.com/liuyngchng/avatar-pc/internal/brain"
)

// Renderer is the window host interface.
type Renderer interface {
	SendMessage(msg any)
	Events() <-chan brain.Event
	// Done returns a channel that is closed when the renderer window has been
	// destroyed (user closed the window, or Close() was called).
	Done() <-chan struct{}
	Close()
}

// New creates the renderer that serves the embedded web/ assets and
// opens a window to display the 3D digital human.
func New(assets embed.FS) (Renderer, error) {
	webFS, err := fs.Sub(assets, "web")
	if err != nil {
		return nil, err
	}
	return newPlatformRenderer(webFS)
}