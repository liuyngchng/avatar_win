//go:build windows

package audio

import (
	"log"
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
			log.Printf("audio: CoInitializeEx: %v", err)
			return
		}
	}
	defer ole.CoUninitialize()

	// --- Enumerate capture device once ---
	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		log.Printf("audio: CoCreateInstance(MMDeviceEnumerator): %v", err)
		return
	}
	defer mmde.Release()

	var mmd *wca.IMMDevice
	if err := mmde.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &mmd); err != nil {
		log.Printf("audio: GetDefaultAudioEndpoint(capture): %v", err)
		return
	}
	defer mmd.Release()

	// --- Activate IAudioClient once ---
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		log.Printf("audio: Activate(IAudioClient): %v", err)
		return
	}
	defer ac.Release()

	// Get the device's mix format (log once).
	var mixFormat *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&mixFormat); err != nil {
		log.Printf("audio: GetMixFormat: %v", err)
		return
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

	streamFlags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM)
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags,
		recorderBufferDuration, 0, requestedFormat, nil); err != nil {
		log.Printf("audio: Initialize with AUTOCONVERTPCM failed: %v; retrying without", err)
		streamFlags = 0
		if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags,
			recorderBufferDuration, 0, requestedFormat, nil); err != nil {
			log.Printf("audio: Initialize: %v", err)
			return
		}
	}

	var bufferFrameSize uint32
	if err := ac.GetBufferSize(&bufferFrameSize); err != nil {
		log.Printf("audio: GetBufferSize: %v", err)
		return
	}

	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		log.Printf("audio: GetService(IAudioCaptureClient): %v", err)
		return
	}
	defer acc.Release()

	if err := ac.Start(); err != nil {
		log.Printf("audio: Start: %v", err)
		return
	}
	defer ac.Stop()

	log.Printf("audio: WASAPI recording started (persistent), native=%d Hz, requested=%d Hz, buffer=%d frames",
		mixFormat.NSamplesPerSec, recorderSampleRate, bufferFrameSize)

	// --- Capture loop: runs until Stop() is called ---
	for {
		select {
		case <-r.stopCh:
			log.Printf("audio: recording stopped (shutdown)")
			return
		default:
		}

		// Drain all available packets.
		hadData := false
		for {
			var packetSize uint32
			if err := acc.GetNextPacketSize(&packetSize); err != nil {
				log.Printf("audio: GetNextPacketSize: %v", err)
				return
			}
			if packetSize == 0 {
				break
			}

			var data *byte
			var framesToRead uint32
			var flags uint32
			if err := acc.GetBuffer(&data, &framesToRead, &flags, nil, nil); err != nil {
				log.Printf("audio: GetBuffer: %v", err)
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