package app

import "sync/atomic"

// Readiness is the process-wide admission state shared by public dispatch and
// health. It starts closed, opens only after required listeners are serving,
// and closes permanently when graceful shutdown begins.
type Readiness struct {
	state atomic.Uint32
}

const (
	readinessStarting uint32 = iota
	readinessServing
	readinessQuiesced
)

func NewReadiness() *Readiness { return &Readiness{} }

func (readiness *Readiness) MarkReady() {
	if readiness != nil {
		readiness.state.CompareAndSwap(readinessStarting, readinessServing)
	}
}

func (readiness *Readiness) Quiesce() {
	if readiness != nil {
		readiness.state.Store(readinessQuiesced)
	}
}

func (readiness *Readiness) IsReady() bool {
	return readiness != nil && readiness.state.Load() == readinessServing
}
