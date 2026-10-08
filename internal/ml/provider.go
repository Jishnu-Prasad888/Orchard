// Package ml implements Cairn's optional local ML capabilities.
//
// Phase 11 ships similarity only: a dependency-free perceptual signature per
// image plus a provider abstraction so future capabilities (faces, learned
// embeddings) can slot in without restructuring callers. Everything is fully
// optional (off by default), runs locally, and produces derived data that can
// be purged and regenerated without touching originals.
package ml

import "image"

// Provider computes similarity signatures for images. A provider identifies
// its algorithm via Name and Version; changing the algorithm bumps Version.
type Provider interface {
	// Name is a stable identifier for the provider, e.g. "average_hash".
	Name() string
	// Version identifies the signature algorithm so clients can reason about
	// compatibility. Changing the algorithm bumps this.
	Version() int
	// Signature derives a signature for a decoded image.
	Signature(img image.Image) (uint64, error)
	// Describe returns a short human-readable summary of the algorithm.
	Describe() string
}
