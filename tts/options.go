package tts

// Options are per synthesis task. Nil pointers preserve provider defaults.
// Speed is a multiplier (1.0 is normal); it is rejected where the provider
// cannot map it reliably. Instruction is a natural-language delivery request.
type Options struct {
	Speed       *float64
	Instruction string
	Provider    ProviderOptions
}

// ProviderOptions holds typed, provider-native controls. A provider must
// reject an options type belonging to another provider.
type ProviderOptions interface {
	TTSProviderOptions()
}
