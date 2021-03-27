package scout

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	api "github.com/rotationalio/scout/proto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	// Initialize zerolog with GCP logging requirements
	zerolog.TimeFieldFormat = time.RFC3339
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}

type Server struct {
	api.UnimplementedScoutServer
	srv    *grpc.Server
	errc   chan error
	events *EventGenerator
}

func New() *Server {
	s := &Server{
		srv:    grpc.NewServer(),
		errc:   make(chan error, 1),
		events: NewEventGenerator(),
	}
	api.RegisterScoutServer(s.srv, s)
	return s
}

func (s *Server) Events(stream api.Scout_EventsServer) (err error) {
	// Get the context of the connection
	ctx := stream.Context()
	done := make(chan error, 1)
	hasClosed := uint32(0)

	// The client has to send an initialization method with the client name in order to
	// begin the event stream handler and the recv message handler. This is a "do once"
	// outside of main event handling loop. This will block until the first message is
	// received or the stream is closed prematurely by the client.
	var report *api.Report
	if report, err = stream.Recv(); err != nil {
		log.Warn().Msg("events channel closed before the first message")
		return status.Errorf(codes.FailedPrecondition, "expected initialization message: %s", err)
	}

	if report.Client == "" {
		log.Warn().Msg("client connected without id")
		return status.Error(codes.FailedPrecondition, "client must initialize with unique name")
	}

	// Add stream to event generator and ensure the stream is cleaned up properly
	// NOTE: all references to the client must be to the first unique name sent in the
	// message, in case client changes its name mid-stream. Do not couple client
	// generated messages to stream keys.
	if err = s.events.AddStream(report.Client, stream, done); err != nil {
		log.Error().Err(err).Str("client", report.Client).Msg("could not add stream to event generator")
		return status.Error(codes.AlreadyExists, err.Error())
	}

	// Cleanup after ourselves when done
	defer func() {
		s.events.RmStream(report.Client)
		atomic.StoreUint32(&hasClosed, 1)
		close(done)
	}()

	// Now that we're initialized and we can send messages back to the client, kick off
	// another routine to handle all subsequent messages from the client.
	go func(expectedClient string) {
		log.Info().Str("client", expectedClient).Msg("client stream reader started")
		for {
			// Receive the message, handling recv errors
			report, err := stream.Recv()

			// Check to ensure done isn't closed and return if so
			if atomic.LoadUint32(&hasClosed) > 0 {
				return
			}

			if err != nil {
				if err == io.EOF {
					done <- nil
				} else {
					done <- err
				}
				return
			}

			// Require client to keep the same unique identity across all messages
			if report.Client != expectedClient {
				log.Warn().Str("request from", report.Client).Str("expected", expectedClient).Msg("unexpected client on stream")
				done <- fmt.Errorf("unexpected client %q (connected as %q)", report.Client, expectedClient)
				return
			}

			// Handle the report from
			if err = handleReport(report); err != nil {
				done <- err
				return
			}
		}
	}(report.Client)

	// Have the outer routine wait for an error or for done and then cleanup and exit
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err = <-done:
		return err
	}
}

func (s *Server) Serve(addr string) (err error) {
	// Listen for CTRL+C and call shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	go func() {
		<-quit
		s.errc <- s.Shutdown()
	}()

	// Kick off the event generator
	s.events.Generate()

	// Listen on the address (ipaddr:port)
	var sock net.Listener
	if sock, err = net.Listen("tcp", addr); err != nil {
		return fmt.Errorf("could not listen on %q: %s", addr, err)
	}
	defer sock.Close()

	// Handle gRPC methods in a go routine
	go func() {
		log.Info().Str("listen", addr).Msg("server started")
		if err := s.srv.Serve(sock); err != nil {
			s.errc <- err
		}
	}()

	// Wait for server error or shutdown
	if err = <-s.errc; err != nil {
		return err
	}
	return nil
}

func (s *Server) Shutdown() (err error) {
	// Shutdown all open client streams
	s.events.CloseAll()

	// Shutdown the gRPC server
	s.srv.GracefulStop()
	return nil
}
