// Package brain contains the state machine and the digital human's
// "mind": mode FSM, emotion mapping, and viseme generation.
//
// This file defines the VRM viseme types and constants used by the
// state machine for lip-sync. The actual viseme driving is done by
// speakWithCancel() in statemachine.go, which cycles through a fixed
// sequence of mouth shapes on a rhythmic timer.
package brain

// VisemeName is a VRM1 viseme (mouth shape) name.
type VisemeName string

const (
	VisemeA    VisemeName = "aa"   // big open mouth
	VisemeI    VisemeName = "ih"   // wide grin
	VisemeU    VisemeName = "ou"   // rounded lips
	VisemeE    VisemeName = "ee"   // half open
	VisemeO    VisemeName = "oh"   // rounded wide
	VisemeRest VisemeName = "rest" // closed mouth (reset all visemes to 0)
)

// VisemeEvent is a viseme instruction sent to the renderer.
type VisemeEvent struct {
	Type   string     `json:"type"`
	Viseme VisemeName `json:"viseme"`
	Weight float64    `json:"weight"`
}