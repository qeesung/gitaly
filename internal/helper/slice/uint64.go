package slice

// Uint64 is a wrapper around slice to provide more functionality in place.
type Uint64 []uint64

// Minus returns new slice that has all elements from left that does not exist at right.
func (l Uint64) Minus(r Uint64) Uint64 {
	if len(l) == 0 {
		return nil
	}

	if len(r) == 0 {
		result := make(Uint64, len(l))
		copy(result, l)
		return result
	}

	excludeSet := make(map[uint64]struct{}, len(l))
	for _, v := range r {
		excludeSet[v] = struct{}{}
	}

	var result Uint64
	for _, v := range l {
		if _, found := excludeSet[v]; !found {
			result = append(result, v)
		}
	}

	return result
}
