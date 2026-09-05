//go:build windows

package audio

import (
	"log/slog"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const (
	recorderSampleRate     = 16000
	recorderBufferDuration = wca.REFERENCE_TIME(30 * 10000) // 30ms in 100ns units
	pollInterval           = 10 * time.Millisecond
)

type windowsRecorder struct {
	mu      sync.Mutex
	ch      chan []float32 // current subscriber; nil when no one is listening
	stopCh  chan struct{}  // closed on final Stop()
	started bool           // whether the persistent capture goroutine is running
	once    sync.Once      // for closing stopCh exactly once
}

// NewRecorder creates a new Windows WASAPI recorder.
// The WASAPI session is initialized on the first Start() call and kept
// alive until Stop() is called on application shutdown.
func NewRecorder() Recorder {
	return &windowsRecorder{}
}

func (r *windowsRecorder) Start() (<-chan []float32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// First call: start the persistent capture goroutine.
	if !r.started {
		r.stopCh = make(chan struct{})
		r.started = true
		go r.captureLoop()
	}

	// Create a fresh subscriber channel.
	ch := make(chan []float32, 32)
	r.ch = ch
	return ch, nil
}

func (r *windowsRecorder) Stop() {
	r.once.Do(func() {
		r.mu.Lock()
		if r.stopCh != nil {
			close(r.stopCh)
		}
		r.mu.Unlock()
	})
}

func (r *windowsRecorder) captureLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// --- Initialize COM once ---
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
			slog.Error("audio: CoInitializeEx", "error", err)
			return
		}
	}
	defer ole.CoUninitialize()

	// --- Enumerate capture device once ---
	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		slog.Error("audio: CoCreateInstance(MMDeviceEnumerator)", "error", err)
		return
	}
	defer mmde.Release()

	var mmd *wca.IMMDevice
	if err := mmde.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &mmd); err != nil {
		slog.Error("audio: GetDefaultAudioEndpoint(capture)", "error", err)
		return
	}
	defer mmd.Release()

	// --- Activate IAudioClient once ---
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		slog.Error("audio: Activate(IAudioClient)", "error", err)
		return
	}
	defer ac.Release()

	// Get the device's mix format (log once).
	var mixFormat *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&mixFormat); err != nil {
		slog.Error("audio: GetMixFormat", "error", err)
		return
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(mixFormat)))
	slog.Info("audio: capture device mix format",
		"tag", mixFormat.WFormatTag,
		"channels", mixFormat.NChannels,
		"rate", mixFormat.NSamplesPerSec,
		"bits", mixFormat.WBitsPerSample)

	// Request 16kHz mono PCM format.
	requestedFormat := &wca.WAVEFORMATEX{
		WFormatTag:     wca.WAVE_FORMAT_PCM,
		NChannels:      1,
		NSamplesPerSec: recorderSampleRate,
		WBitsPerSample: 16,
		NBlockAlign:    2,
		NAvgBytesPerSec: recorderSampleRate * 2,
		CbSize:         0,
	}

	streamFlags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM)
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags,
		recorderBufferDuration, 0, requestedFormat, nil); err != nil {
		slog.Warn("audio: Initialize with AUTOCONVERTPCM failed, retrying without", "error", err)
		streamFlags = 0
		if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags,
			recorderBufferDuration, 0, requestedFormat, nil); err != nil {
			slog.Error("audio: Initialize", "error", err)
			return
		}
	}

	var bufferFrameSize uint32
	if err := ac.GetBufferSize(&bufferFrameSize); err != nil {
		slog.Error("audio: GetBufferSize", "error", err)
		return
	}

	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		slog.Error("audio: GetService(IAudioCaptureClient)", "error", err)
		return
	}
	defer acc.Release()

	if err := ac.Start(); err != nil {
		slog.Error("audio: Start", "error", err)
		return
	}
	defer ac.Stop()

	slog.Info("audio: WASAPI recording started (persistent)",
		"native_hz", mixFormat.NSamplesPerSec,
		"requested_hz", recorderSampleRate,
		"buffer_frames", bufferFrameSize)

	// --- Capture loop: runs until Stop() is called ---
	for {
		select {
		case <-r.stopCh:
			slog.Info("audio: recording stopped (shutdown)")
			return
		default:
		}

		// Drain all available packets.
		hadData := false
		for {
			var packetSize uint32
			if err := acc.GetNextPacketSize(&packetSize); err != nil {
				slog.Error("audio: GetNextPacketSize", "error", err)
				return
			}
			if packetSize == 0 {
				break
			}

			var data *byte
			var framesToRead uint32
			var flags uint32
			if err := acc.GetBuffer(&data, &framesToRead, &flags, nil, nil); err != nil {
				slog.Error("audio: GetBuffer", "error", err)
				return
			}

			if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT == 0 && data != nil && framesToRead > 0 {
				floatSamples := make([]float32, int(framesToRead))
				int16Ptr := (*[1 << 30]int16)(unsafe.Pointer(data))[:framesToRead:framesToRead]
				for i := range int(framesToRead) {
					floatSamples[i] = float32(int16Ptr[i]) / math.MaxInt16
				}
				hadData = true

				// Non-blocking send: drop if nobody is listening.
				r.mu.Lock()
				ch := r.ch
				r.mu.Unlock()
				if ch != nil {
					select {
					case ch <- floatSamples:
					default:
						// Consumer is too slow or channel is full; drop.
					}
				}
			}

			acc.ReleaseBuffer(framesToRead)
		}

		if !hadData {
			time.Sleep(pollInterval)
		}
	}
}