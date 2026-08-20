//go:build linux

package audio

import (
	"encoding/binary"
	"log"
	"math"

	"github.com/gen2brain/malgo"
)

const recorderSampleRate = 16000

type linuxRecorder struct {
	ctx    *malgo.AllocatedContext
	device *malgo.Device
	stopCh chan struct{}
}

// NewRecorder creates a new Linux malgo recorder.
func NewRecorder() Recorder {
	return &linuxRecorder{}
}

func (r *linuxRecorder) Start() (<-chan []float32, error) {
	var err error

	r.ctx, err = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, err
	}

	r.stopCh = make(chan struct{})
	samples := make(chan []float32, 32)

	config := malgo.DefaultDeviceConfig(malgo.Capture)
	config.Capture.Format = malgo.FormatS16
	config.Capture.Channels = 1
	config.SampleRate = recorderSampleRate
	config.PeriodSizeInMilliseconds = 10
	config.Periods = 3

	deviceCallbacks := malgo.DeviceCallbacks{
		Data: func(_, pInputSamples []byte, framecount uint32) {
			select {
			case <-r.stopCh:
				return
			default:
			}

			if pInputSamples == nil || framecount == 0 {
				return
			}

			floatSamples := make([]float32, int(framecount))
			for i := range int(framecount) {
				s := int16(binary.LittleEndian.Uint16(pInputSamples[i*2 : i*2+2]))
				floatSamples[i] = float32(s) / math.MaxInt16
			}

			select {
			case samples <- floatSamples:
			case <-r.stopCh:
			}
		},
		Stop: func() {
			log.Printf("audio: malgo device stopped")
		},
	}

	r.device, err = malgo.InitDevice(r.ctx.Context, config, deviceCallbacks)
	if err != nil {
		r.ctx.Free()
		return nil, err
	}

	if err := r.device.Start(); err != nil {
		r.device.Uninit()
		r.ctx.Free()
		return nil, err
	}

	log.Printf("audio: malgo recording started, %d Hz", recorderSampleRate)
	return samples, nil
}

func (r *linuxRecorder) Stop() {
	if r.device != nil {
		r.device.Stop()
		r.device.Uninit()
		r.device = nil
	}
	if r.ctx != nil {
		r.ctx.Free()
		r.ctx = nil
	}
	if r.stopCh != nil {
		close(r.stopCh)
	}
}