package main

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: glue-packs PACK1 < PACK2")
		os.Exit(1)
	}

	if err := _main(); err != nil {
		log.Fatal(err)
	}
}

func _main() error {
	pack1, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	defer pack1.Close()

	pack1Reader, err := NewPackReader(pack1)
	if err != nil {
		return err
	}

	nPack1 := pack1Reader.NumObjects()
	log.Printf("%s: %d objects", os.Args[1], nPack1)

	pack2Reader, err := NewPackReader(os.Stdin)
	if err != nil {
		return err
	}

	nPack2 := pack2Reader.NumObjects()
	log.Printf("stdin: %d objects", nPack2)

	summer := sha1.New()
	stdout := io.MultiWriter(os.Stdout, summer)

	if _, err := fmt.Fprint(stdout, packMagic); err != nil {
		return err
	}

	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, nPack1+nPack2) // TODO check for overflow
	if _, err := stdout.Write(size); err != nil {
		return err
	}

	if _, err := io.Copy(stdout, pack1Reader); err != nil {
		return err
	}

	if _, err := io.Copy(stdout, pack2Reader); err != nil {
		return err
	}

	if _, err := stdout.Write(summer.Sum(nil)); err != nil {
		return err
	}

	return nil
}
