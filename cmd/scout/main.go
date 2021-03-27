package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/rotationalio/scout"
	api "github.com/rotationalio/scout/proto"
	cli "github.com/urfave/cli/v2"
	"google.golang.org/grpc"
)

func main() {
	app := cli.NewApp()
	app.Name = "scout"
	app.Usage = "stream management test code"
	app.Version = "beta"
	app.Flags = []cli.Flag{}

	app.Commands = []*cli.Command{
		{
			Name:     "serve",
			Usage:    "run the scout server",
			Category: "server",
			Action:   serve,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "addr",
					Aliases: []string{"a"},
					Usage:   "address to bind the server on",
					Value:   ":831",
				},
			},
		},
		{
			Name:     "events",
			Usage:    "run an events handler client",
			Category: "client",
			Action:   events,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "endpoint",
					Aliases: []string{"e"},
					Usage:   "endpoint to connect to the server on",
					Value:   "localhost:831",
				},
				&cli.StringFlag{
					Name:    "client",
					Aliases: []string{"c"},
					Usage:   "unique name of client",
					Value:   "saucy",
				},
			},
		},
	}

	app.Run(os.Args)
}

func serve(c *cli.Context) (err error) {
	server := scout.New()
	if err = server.Serve(c.String("addr")); err != nil {
		return cli.Exit(err, 1)
	}
	return nil
}

func events(c *cli.Context) (err error) {
	var cc *grpc.ClientConn
	if cc, err = grpc.Dial(c.String("endpoint"), grpc.WithInsecure()); err != nil {
		return cli.Exit(err, 1)
	}
	defer cc.Close()

	var stream api.Scout_EventsClient
	client := api.NewScoutClient(cc)
	name := c.String("client")

	if stream, err = client.Events(context.Background()); err != nil {
		return cli.Exit(err, 1)
	}

	// Send the initalization message
	report := &api.Report{Success: true, Client: name}
	if err = stream.Send(report); err != nil {
		return cli.Exit(err, 1)
	}

	// Listen for events and respond to them
	for {
		var event *api.Event
		if event, err = stream.Recv(); err != nil {
			return cli.Exit(err, 1)
		}

		fmt.Println(event)
		time.Sleep(time.Duration(rand.Int63n(2000)) * time.Millisecond)

		success := !strings.HasPrefix(event.Event, "f")
		if err = stream.Send(&api.Report{MsgId: event.MsgId, Success: success, Client: name}); err != nil {
			return cli.Exit(err, 1)
		}
	}
}
