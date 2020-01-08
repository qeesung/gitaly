package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

const (
	progName = "gitalyfmt"
)

var (
	writeFiles = flag.Bool("w", false, "write changes to inspected files")
)

func main() {
	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Fprintf(os.Stderr, "usage: %s file.go [file.go...]\n", progName)
		os.Exit(1)
	}

	if err := _main(flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "%s: fatal: %v", progName, err)
		os.Exit(1)
	}
}

func _main(args []string) error {
	for _, f := range args {
		fi, err := os.Stat(f)
		if err != nil {
			return err
		}

		src, err := ioutil.ReadFile(f)
		if err != nil {
			return err
		}

		dst := format(src)
		if bytes.Equal(src, dst) {
			continue
		}

		fmt.Println(f)

		if !*writeFiles {
			continue
		}

		if err := ioutil.WriteFile(f, dst, fi.Mode()); err != nil {
			return err
		}
	}

	return nil
}
