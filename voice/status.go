// Package voice contains shared voice enrollment and design result types.
// Voice resources are independent of speech synthesis clients.
package voice

// State describes a provider-reported voice state, not account entitlement.
type State string

const (
	Unknown    State = "unknown"
	NotFound   State = "not_found"
	Ready      State = "ready"
	Active     State = "active"
	Processing State = "processing"
	Rejected   State = "rejected"
)

// Usable reports whether the provider reports a synthesis-ready state.
// The target model and account must still support the voice.
func (s State) Usable() bool {
	return s == Ready || s == Active
}
