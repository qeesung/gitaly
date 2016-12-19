package server

import (
	"io"
	"log"
	"net"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/messaging"
)

type Server struct {
	ch        chan bool
	waitGroup *sync.WaitGroup
}

func NewServer() *Server {
	server := &Server{
		ch:        make(chan bool),
		waitGroup: &sync.WaitGroup{},
	}
	server.waitGroup.Add(1)
	return server
}

func (s *Server) Serve(address string, service Service) {
	listener, err := newListener(address)
	if err != nil {
		log.Fatalln(err)
	}
	defer s.waitGroup.Done()
	log.Println("Listening on address", address)
	for {
		select {
		case <-s.ch:
			log.Println("Received shutdown message, stopping server on", listener.Addr())
			listener.Close()
			return
		default:
		}
		listener.SetDeadline(time.Now().Add(1e9))
		conn, err := listener.AcceptTCP()
		if err != nil {
			if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
				continue
			}
			log.Println(err)
		}
		log.Println("Client connected from ", conn.RemoteAddr())
		s.waitGroup.Add(1)

		chans := newCommChans()
		go service(chans)
		go s.serve(conn, chans)
	}
}

func newListener(address string) (*net.TCPListener, error) {
	tcpAddress, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return &net.TCPListener{}, err
	}
	return net.ListenTCP("tcp", tcpAddress)
}

func (s *Server) Stop() {
	close(s.ch)
	s.waitGroup.Wait()
}

func (s *Server) serve(conn *net.TCPConn, chans *commChans) {
	defer conn.Close()
	defer s.waitGroup.Done()

	messagesConn := messaging.NewMessagesConn(conn)

	go func() {
		for {
			ret, ok := <-chans.outChan
			if !ok {
				return
			}

			if _, err := messagesConn.Write(ret); nil != err {
				log.Println(err)
				return
			}
		}
	}()

	for {
		select {
		case <-s.ch:
			log.Println("Received shutdown message, disconnecting client from", conn.RemoteAddr())
			return
		default:
		}

		conn.SetDeadline(time.Now().Add(1e9))

		buffer, err := messagesConn.Read()
		if err != nil {
			if err == io.EOF {
				log.Println("Client", conn.RemoteAddr(), "closed the connection")
				return
			}
			if opError, ok := err.(*net.OpError); ok && opError.Timeout() {
				continue
			}
			log.Println(err)
		}

		chans.inChan <- buffer
	}

	chans.Close()
}
