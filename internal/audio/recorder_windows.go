//go:build windows

package audio

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const (
	recorderSampleRate     = 16000
	recorderBufferDuration = wca.REFERENCE_TIME(30 * 10000) // 30ms in 100ns units
)

type windowsRecorder struct {
	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewRecorder creates a new Windows WASAPI recorder.
func NewRecorder() Recorder {
	return &windowsRecorder{}
}

func (r *windowsRecorder) Start() (<-chan []float32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stopCh = make(chan struct{})
	r.stopOnce = sync.Once{}

	samples := make(chan []float32, 32)

	go func() {
		defer close(samples)
		if err := r.captureLoop(samples); err != nil {
			log.Printf("audio: recorder error: %v", err)
		}
	}()

	return samples, nil
}

func (r *windowsRecorder) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
}

func (r *windowsRecorder) captureLoop(samples chan<- []float32) error {
	// Initialize COM for this goroutine.
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		return fmt.Errorf("CoInitializeEx: %w", err)
	}
	defer ole.CoUninitialize()

	// Create device enumerator.
	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		return fmt.Errorf("CoCreateInstance(MMDeviceEnumerator): %w", err)
	}
	defer mmde.Release()

	// Get default capture device.
	var mmd *wca.IMMDevice
	if err := mmde.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &mmd); err != nil {
		return fmt.Errorf("GetDefaultAudioEndpoint(capture): %w", err)
	}
	defer mmd.Release()

	// Activate IAudioClient.
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return fmt.Errorf("Activate(IAudioClient): %w", err)
	}
	defer ac.Release()

	// Get the device's mix format.
	var mixFormat *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&mixFormat); err != nil {
		return fmt.Errorf("GetMixFormat: %w", err)
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(mixFormat)))
	log.Printf("audio: capture device mix format: tag=%d, channels=%d, rate=%d, bits=%d",
		mixFormat.WFormatTag, mixFormat.NChannels, mixFormat.NSamplesPerSec, mixFormat.WBitsPerSample)

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

	// Use polling mode (no event callback) for maximum compatibility.
	// Some devices/drivers don't support AUDCLNT_STREAMFLAGS_EVENTCALLBACK.
	streamFlags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM)

	log.Printf("audio: initializing in polling mode (no event callback)...")
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags,
		recorderBufferDuration, 0, requestedFormat, nil); err != nil {
		// Retry without AUTOCONVERTPCM — some devices/drivers reject it.
		log.Printf("audio: Initialize with AUTOCONVERTPCM failed: %v; retrying without", err)
		streamFlags = 0
		if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags,
			recorderBufferDuration, 0, requestedFormat, nil); err != nil {
			return fmt.Errorf("Initialize: %w", err)
		}
	}

	// Get buffer size.
	var bufferFrameSize uint32
	if err := ac.GetBufferSize(&bufferFrameSize); err != nil {
		return fmt.Errorf("GetBufferSize: %w", err)
	}

	// Get capture client.
	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		return fmt.Errorf("GetService(IAudioCaptureClient): %w", err)
	}
	defer acc.Release()

	// Start recording.
	if err := ac.Start(); err != nil {
		return fmt.Errorf("Start: %w", err)
	}
	defer ac.Stop()

	log.Printf("audio: WASAPI recording started (polling), native=%d Hz, requested=%d Hz, buffer=%d frames",
		mixFormat.NSamplesPerSec, recorderSampleRate, bufferFrameSize)

	// Capture loop: poll for audio packets every 10ms.
	pollInterval := 10 * time.Millisecond
	for {
		select {
		case <-r.stopCh:
			log.Printf("audio: recording stopped")
			return nil
		default:
		}

		// Drain all available packets.
		for {
			var packetSize uint32
			if err := acc.GetNextPacketSize(&packetSize); err != nil {
				return fmt.Errorf("GetNextPacketSize: %w", err)
			}
			if packetSize == 0 {
				break
			}

			var data *byte
			var framesToRead uint32
			var flags uint32
			if err := acc.GetBuffer(&data, &framesToRead, &flags, nil, nil); err != nil {
				return fmt.Errorf("GetBuffer: %w", err)
			}

			if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT == 0 && data != nil && framesToRead > 0 {
				// Convert int16 PCM to float32.
				floatSamples := make([]float32, int(framesToRead))
				int16Ptr := (*[1 << 30]int16)(unsafe.Pointer(data))[:framesToRead:framesToRead]
				for i := range int(framesToRead) {
					floatSamples[i] = float32(int16Ptr[i]) / math.MaxInt16
				}

				select {
				case samples <- floatSamples:
				case <-r.stopCh:
					acc.ReleaseBuffer(framesToRead)
					return nil
				}
			}

			acc.ReleaseBuffer(framesToRead)
		}

		// Small sleep to avoid busy-waiting.
		time.Sleep(pollInterval)
	}
}