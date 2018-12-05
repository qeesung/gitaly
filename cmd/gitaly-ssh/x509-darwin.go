// +build darwin
//
// This is test-only code to circumvent the fact that Go on macOS does
// not support SSL_CERT_{DIR,FILE}.
//

package main

import (
	"crypto/x509"
	"io/ioutil"
	"log"
	"os"
	"path"
)

func init() {
	var certPem []byte
	if f := os.Getenv("SSL_CERT_FILE"); len(f) > 0 {
		pem, err := ioutil.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		pem = append(pem, '\n')
		certPem = append(certPem, pem...)
	}

	if d := os.Getenv("SSL_CERT_DIR"); len(d) > 0 {
		entries, err := ioutil.ReadDir(d)
		if err != nil {
			log.Fatal(err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			pem, err := ioutil.ReadFile(path.Join(d, entry.Name()))
			if err != nil {
				log.Fatal(err)
			}
			pem = append(pem, '\n')
			certPem = append(certPem, pem...)
		}
	}

	if len(certPem) == 0 {
		return
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		log.Fatal(err)
	}

	pool.AppendCertsFromPEM(certPem)
}
