package metrics

// BoolAsFloat is a utility for converting a boolean value to a float64
// for Prometheus. Returns 1 if bool is true, else 0.
func BoolAsFloat(b bool) float64 {
	if b {
		return 1
	}

	return 0
}
