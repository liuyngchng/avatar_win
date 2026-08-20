//go:build windows

package audio

import (
	"log"
	"math"
	"sync"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const (
	recorderSampleRate     = 16000
	recorderBufferDuration = wca.REFERENCE_TIME(30 * 10000) // 30ms in 100ns units
	recorderPeriodicity    = wca.REFERENCE_TIME(10 * 10000) // 10ms
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
		return err
	}
	defer ole.CoUninitialize()

	// Create device enumerator.
	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		// Fallback: try the non-COM version.
		return err
	}
	defer mmde.Release()

	// Get default capture device.
	var mmd *wca.IMMDevice
	if err := mmde.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &mmd); err != nil {
		return err
	}
	defer mmd.Release()

	// Activate IAudioClient.
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return err
	}
	defer ac.Release()

	// Get the device's mix format to know its native sample rate.
	var mixFormat *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&mixFormat); err != nil {
		return err
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(mixFormat)))
	nativeSampleRate := mixFormat.NSamplesPerSec

	// Request 16kHz mono PCM format.
	// We let WASAPI auto-convert (AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM).
	requestedFormat := &wca.WAVEFORMATEX{
		WFormatTag:     wca.WAVE_FORMAT_PCM,
		NChannels:      1,
		NSamplesPerSec: recorderSampleRate,
		WBitsPerSample: 16,
		NBlockAlign:    2,
		NAvgBytesPerSec: recorderSampleRate * 2,
		CbSize:         0,
	}

	streamFlags := uint32(wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK | wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM)

	// Create event handle.
	eventHandle := wca.CreateEventExA(0, 0, 0, wca.EVENT_MODIFY_STATE|wca.SYNCHRONIZE)
	if eventHandle == 0 {
		return syscall.GetLastError()
	}
	defer wca.CloseHandle(eventHandle)

	if err := ac.SetEventHandle(eventHandle); err != nil {
		return err
	}

	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags,
		recorderBufferDuration, 0, requestedFormat, nil); err != nil {
		return err
	}

	// Get buffer size.
	var bufferFrameSize uint32
	if err := ac.GetBufferSize(&bufferFrameSize); err != nil {
		return err
	}

	// Get capture client.
	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		return err
	}
	defer acc.Release()

	// Start recording.
	if err := ac.Start(); err != nil {
		return err
	}
	defer ac.Stop()

	log.Printf("audio: WASAPI recording started, native=%d Hz, requested=%d Hz, buffer=%d frames",
		nativeSampleRate, recorderSampleRate, bufferFrameSize)

	// Capture loop: wait for event, then read all available packets.
	for {
		select {
		case <-r.stopCh:
			log.Printf("audio: recording stopped")
			return nil
		default:
		}

		// Wait for the next audio buffer with a short timeout so we can
		// check the stop signal.
		dword := wca.WaitForSingleObject(eventHandle, 100)
		if dword != 0 {
			// Timeout or error — check stop signal and retry.
			continue
		}

		// Drain all available packets.
		for {
			var packetSize uint32
			if err := acc.GetNextPacketSize(&packetSize); err != nil {
				return err
			}
			if packetSize == 0 {
				break
			}

			var data *byte
			var framesToRead uint32
			var flags uint32
			if err := acc.GetBuffer(&data, &framesToRead, &flags, nil, nil); err != nil {
				return err
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
	}
}