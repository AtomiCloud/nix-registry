package tempo

import "github.com/AtomiCloud/obs/internal/seed"

type TAttrVal = seed.TAttrVal
type TSpan = seed.TSpan
type TTrace = seed.TTrace
type Label = seed.Label

// Result is a TraceQL metrics series; points are Unix milliseconds.
type Result struct {
	Labels []Label
	Points map[int64]float64
}
