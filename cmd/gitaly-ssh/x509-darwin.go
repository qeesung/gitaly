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
	count := 0

	if f := os.Getenv("SSL_CERT_FILE"); len(f) > 0 {
		pem, err := ioutil.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		pem = append(pem, '\n')
		certPem = append(certPem, pem...)
		count++
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
			count++
		}
	}

	if len(certPem) == 0 {
		return
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		log.Fatal(err)
	}

	if pool.AppendCertsFromPEM(certPem) {
		log.Printf("added %d certificates to pool", count)
	} else {
		log.Printf("failed to add %d certificates to pool", count)
	}
}
