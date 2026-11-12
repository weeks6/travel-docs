// Package natstest runs an embedded NATS server for tests.
package natstest

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// Server is an embedded NATS server.
type Server struct {
	*server.Server
	port int
}

// Run starts a server on a random local port and stops it when the test
// ends.
func Run(t testing.TB) *Server {
	t.Helper()
	s := start(t, -1)
	srv := &Server{Server: s, port: s.Addr().(*net.TCPAddr).Port}
	t.Cleanup(func() { srv.Shutdown() })
	return srv
}

// URL is the client URL of the server.
func (s *Server) URL() string { return fmt.Sprintf("nats://127.0.0.1:%d", s.port) }

// Restart stops the server and starts a new one on the same port.
func (s *Server) Restart(t testing.TB) {
	t.Helper()
	s.Shutdown()
	s.WaitForShutdown()
	s.Server = start(t, s.port)
}

func start(t testing.TB, port int) *server.Server {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: port, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not start")
	}
	return s
}
