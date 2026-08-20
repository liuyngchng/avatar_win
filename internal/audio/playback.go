// Package audio provides PCM audio playback using oto (ALSA on Linux,
// WASAPI on Windows).
package audio

import (
	"bytes"
	"encoding/binary"
	"log"
	"math"
	"time"

	"github.com/ebitengine/oto/v3"
)

// Player plays PCM float32 audio through the default audio device.
type Player struct {
	ctx   *oto.Context
	ready chan struct{}
}

// NewPlayer creates a new audio player with the given sample rate.
func NewPlayer(sampleRate int) (*Player, error) {
	opts := &oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: 1, // mono
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   0, // use driver default
	}
	ctx, ready, err := oto.NewContext(opts)
	if err != nil {
		return nil, err
	}
	log.Printf("audio: player created, sample_rate=%d", sampleRate)
	return &Player{ctx: ctx, ready: ready}, nil
}

// WaitReady blocks until the audio context is ready.
func (p *Player) WaitReady() {
	<-p.ready
	log.Printf("audio: player ready")
}

// float32ToBytes converts normalized float32 samples in [-1, 1] to
// little-endian int16 PCM bytes.
func float32ToBytes(samples []float32) []byte {
	var buf bytes.Buffer
	for _, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		v := int16(s * math.MaxInt16)
		binary.Write(&buf, binary.LittleEndian, v)
	}
	return buf.Bytes()
}

// Play starts playing the given float32 PCM samples without blocking.
// The audio plays in the background; use Wait to block until done.
func (p *Player) Play(samples []float32) (*oto.Player, error) {
	player := p.ctx.NewPlayer(bytes.NewReader(float32ToBytes(samples)))
	player.Play()
	return player, nil
}

// PlaySync plays the given float32 PCM samples and blocks until done.
// samples must be normalized in [-1, 1].
func (p *Player) PlaySync(samples []float32) error {
	player, err := p.Play(samples)
	if err != nil {
		return err
	}
	return waitDone(player)
}

// waitDone blocks until the given player finishes playing.
func waitDone(player *oto.Player) error {
	for player.IsPlaying() {
		if err := player.Err(); err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return player.Err()
}

// Close releases the audio context.
func (p *Player) Close() {
	// oto Context doesn't have an explicit Close; GC handles cleanup.
}