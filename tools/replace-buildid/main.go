// The `replace-buildid` tool is used to replace a build ID in an ELF binary with a new build ID.
// Note that this tool is extremely naive: it simply takes the old input ID as string, verifies
// that this ID is contained in the binary exactly once, and then replaces it. It has no knowledge
// about ELF binaries whatsoever.
//
// This tool is mainly used to replace our static GNU build ID we set in our Makefile with a
// derived build ID without having to build binaries twice.

package main

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	var inputPath, outputPath, inputBuildID, outputBuildID string

	flag.StringVar(&inputPath, "input", "", "path to the binary whose GNU build ID should be replaced")
	flag.StringVar(&outputPath, "output", "", "path whether the resulting binary should be placed")
	flag.StringVar(&inputBuildID, "input-build-id", "", "static build ID to replace")
	flag.StringVar(&outputBuildID, "output-build-id", "", "new build ID to replace old value with")
	flag.Parse()

	if err := replaceBuildID(inputPath, outputPath, inputBuildID, outputBuildID); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func replaceBuildID(inputPath, outputPath, inputBuildID, outputBuildID string) error {
	if inputPath == "" {
		return fmt.Errorf("missing input path")
	}
	if outputPath == "" {
		return fmt.Errorf("missing output path")
	}
	if inputBuildID == "" {
		return fmt.Errorf("missing output path")
	}
	if outputBuildID == "" {
		return fmt.Errorf("missing output path")
	}
	if flag.NArg() > 0 {
		return fmt.Errorf("extra arguments")
	}

	inputBuildIDDecoded, err := hex.DecodeString(inputBuildID)
	if err != nil {
		return fmt.Errorf("decoding input build ID: %w", err)
	}

	outputBuildIDDecoded, err := hex.DecodeString(outputBuildID)
	if err != nil {
		return fmt.Errorf("decoding output build ID: %w", err)
	}

	if len(inputBuildIDDecoded) != len(outputBuildIDDecoded) {
		return fmt.Errorf("input and output build IDs do not have the same length")
	}

	data, err := readAndReplace(inputPath, inputBuildIDDecoded, outputBuildIDDecoded)
	if err != nil {
		return fmt.Errorf("could not replace build ID: %w", err)
	}

	if err := writeBinary(inputPath, outputPath, data); err != nil {
		return fmt.Errorf("writing binary: %w", err)
	}

	return nil
}

func readAndReplace(binaryPath string, inputBuildID, outputBuildID []byte) ([]byte, error) {
	inputFile, err := os.Open(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("opening input file: %w", err)
	}
	defer inputFile.Close()

	data, err := io.ReadAll(inputFile)
	if err != nil {
		return nil, fmt.Errorf("reading input file: %w", err)
	}

	if occurrences := bytes.Count(data, inputBuildID); occurrences != 1 {
		return nil, fmt.Errorf("exactly one match for old build ID expected, got %d", occurrences)
	}

	return bytes.ReplaceAll(data, inputBuildID, outputBuildID), nil
}

func writeBinary(inputPath string, binaryPath string, contents []byte) error {
	f, err := os.CreateTemp(filepath.Dir(binaryPath), filepath.Base(binaryPath))
	if err != nil {
		return fmt.Errorf("could not create binary: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(f.Name())
		f.Close()
	}()

	if _, err := io.Copy(f, bytes.NewReader(contents)); err != nil {
		return fmt.Errorf("could not write binary: %w", err)
	}

	inputStat, err := os.Stat(inputPath)
	if err != nil {
		return fmt.Errorf("stat input: %w", err)
	}

	if err := f.Chmod(inputStat.Mode()); err != nil {
		return fmt.Errorf("could not change permissions: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("could not close binary: %w", err)
	}

	if err := os.Rename(f.Name(), binaryPath); err != nil {
		return fmt.Errorf("could not move binary into place: %w", err)
	}

	return nil
}
