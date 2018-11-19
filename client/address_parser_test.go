package client

import (
	"testing"
)

func TestParseAddress(t *testing.T) {
	testCases := []struct {
		raw       string
		canonical string
		invalid   bool
		secure    bool
	}{
		{raw: "unix:/foo/bar.socket", canonical: "unix:///foo/bar.socket", secure: false},
		{raw: "unix:///foo/bar.socket", canonical: "unix:///foo/bar.socket", secure: false},
		// Mainly for test purposes we explicitly want to support relative paths
		{raw: "unix://foo/bar.socket", canonical: "unix://foo/bar.socket", secure: false},
		{raw: "unix:foo/bar.socket", canonical: "unix:foo/bar.socket", secure: false},
		{raw: "tcp://1.2.3.4", canonical: "1.2.3.4", secure: false},
		{raw: "tcp://1.2.3.4:567", canonical: "1.2.3.4:567", secure: false},
		{raw: "tcp://foobar", canonical: "foobar", secure: false},
		{raw: "tcp://foobar:567", canonical: "foobar:567", secure: false},
		{raw: "tcp://1.2.3.4/foo/bar.socket", invalid: true},
		{raw: "tcp:///foo/bar.socket", invalid: true},
		{raw: "tcp:/foo/bar.socket", invalid: true},
		{raw: "tcp://[2001:0db8:85a3:0000:0000:8a2e:0370:7334]:9999", canonical: "[2001:0db8:85a3:0000:0000:8a2e:0370:7334]:9999", secure: false},
		{raw: "foobar:9999", canonical: "foobar:9999", secure: false},
		{raw: "https://foobar:9999", canonical: "https://foobar:9999", secure: true},
	}

	for _, tc := range testCases {
		canonical, secure, err := parseAddress(tc.raw)

		if err == nil && tc.invalid {
			t.Errorf("%v: expected error, got none", tc)
		} else if err != nil && !tc.invalid {
			t.Errorf("%v: parse error: %v", tc, err)
			continue
		}

		if tc.invalid {
			continue
		}

		if tc.canonical != canonical {
			t.Errorf("%v: expected %q, got %q", tc, tc.canonical, canonical)
		}

		if tc.secure != secure {
			t.Errorf("%v: expected %v, got %v", tc, tc.secure, secure)
		}
	}
}
