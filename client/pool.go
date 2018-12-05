// +build !darwin
package client

import "crypto/x509"

func systemCertPool() (*x509.CertPool, error) { return x509.SystemCertPool() }
