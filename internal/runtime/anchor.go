package runtime

// FinalizedAnchorOption tunes the finalized preset's establishment.
type FinalizedAnchorOption func(*FinalizedAnchorConfig)

// FinalizedAnchorConfig carries the finalized preset's knobs.
type FinalizedAnchorConfig struct{ MaxLag uint64 }

// DefaultMaxFinalizedLag is the documented lag bound of the finalized preset.
const DefaultMaxFinalizedLag = 32

// WithMaxFinalizedLag bounds lag behind the lower-median reported height.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return func(c *FinalizedAnchorConfig) { c.MaxLag = blocks }
}

// FinalizedAnchorLag extracts the lag bound an option list selects.
func FinalizedAnchorLag(opts ...FinalizedAnchorOption) uint64 {
	cfg := FinalizedAnchorConfig{MaxLag: DefaultMaxFinalizedLag}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.MaxLag
}
