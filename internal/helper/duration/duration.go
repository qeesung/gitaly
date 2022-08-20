package duration

import "time"

// Duration is a trick to let our TOML library parse durations from strings.
type Duration time.Duration

// Duration converts the duration.Duration to a time.Duration
func (d *Duration) Duration() time.Duration {
	if d != nil {
		return time.Duration(*d)
	}
	return 0
}

// UnmarshalText implements the encoding.TextUnmarshaler interface
func (d *Duration) UnmarshalText(text []byte) error {
	td, err := time.ParseDuration(string(text))
	if err == nil {
		*d = Duration(td)
	}
	return err
}

// MarshalText implements the encoding.TextMarshaler interface
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}
