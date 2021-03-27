package scout

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	api "github.com/rotationalio/scout/proto"
	"github.com/rs/zerolog/log"
)

func handleReport(report *api.Report) (err error) {
	log.Info().Uint64("event", report.MsgId).Bool("success", report.Success).Msg("reported")
	if !report.Success {
		return fmt.Errorf("%q reports failure, stopping handler", report.Client)
	}
	return nil
}

// EventGenerator creates random events and sends them down all available streams.
type EventGenerator struct {
	sync.Mutex
	eventId   uint64
	streams   map[string]api.Scout_EventsServer
	closers   map[string]chan<- error
	generator sync.Once
}

func NewEventGenerator() *EventGenerator {
	return &EventGenerator{
		streams: make(map[string]api.Scout_EventsServer),
		closers: make(map[string]chan<- error),
	}
}

func (e *EventGenerator) AddStream(client string, stream api.Scout_EventsServer, close chan<- error) error {
	e.Lock()
	defer e.Unlock()

	if _, ok := e.streams[client]; ok {
		return fmt.Errorf("already a stream named %q", client)
	}

	e.streams[client] = stream
	e.closers[client] = close
	log.Debug().Str("client", client).Msg("event generator added stream")
	return nil
}

func (e *EventGenerator) RmStream(client string) {
	e.Lock()
	defer e.Unlock()
	if _, ok := e.streams[client]; ok {
		log.Debug().Str("client", client).Msg("event generator removed client")
		delete(e.streams, client)
		delete(e.closers, client)
		return
	}
	log.Debug().Str("client", client).Msg("unknown client stream")
}

func (e *EventGenerator) CloseAll() {
	e.Lock()
	defer e.Unlock()
	for client := range e.streams {
		e.closeStream(client)
	}
}

func (e *EventGenerator) CloseStream(client string) {
	e.Lock()
	defer e.Unlock()
	e.closeStream(client)
}

func (e *EventGenerator) closeStream(client string) {
	if close, ok := e.closers[client]; ok {
		log.Debug().Str("client", client).Msg("closing client")
		close <- errors.New("only sends up to 10 events at a time")
		return
	}
	log.Debug().Str("client", client).Msg("unknown client context")
}

func (e *EventGenerator) Generate() {
	e.generator.Do(func() {
		go func() {
			for {
				time.Sleep(time.Duration(rand.Int63n(4000)) * time.Millisecond)
				e.generate()

				// Close streams after 100 messages have been sent
				if e.eventId%100 == 0 {
					e.CloseAll()
				}
			}
		}()
	})
}

func (e *EventGenerator) generate() {
	e.eventId++
	event := &api.Event{
		MsgId:     e.eventId,
		Event:     fmt.Sprintf("%x", rand.Int63n(1e9)),
		Timestamp: time.Now().Format(time.RFC3339),
	}

	e.Lock()
	defer e.Unlock()
	for client, stream := range e.streams {
		if err := stream.Send(event); err != nil {
			// NOTE: no need to remove the stream here, it will eventually be removed
			// by the stream handler if it's closed or erroring (in the cleanup).
			log.Error().Err(err).Str("client", client).Msg("could not send event")
		}
	}

}
