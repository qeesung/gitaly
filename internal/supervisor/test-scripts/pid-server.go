package main

import (
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	if err := os.RemoveAll("socket"); err != nil {
		log.Fatal(err)
	}

	l, err := net.Listen("unix", "socket")
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Print(err)
			continue
		}

		if _, err := fmt.Fprintf(conn, "%d", os.Getpid()); err != nil {
			log.Print(err)
		}

		conn.Close()
	}
}
