// Command trafficlight logs the traffic a mitm sends it.
//
//	trafficlight              run it (also: trafficlight run)
//	trafficlight healthcheck  exit 0 if a running Traffic Light takes requests
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	// time zones for the TZ variable, so exports carry your local time even in an image
	// that has no time zone database
	_ "time/tzdata"

	"github.com/pokemaplx/TrafficLight/internal/config"
	"github.com/pokemaplx/TrafficLight/internal/forward"
	"github.com/pokemaplx/TrafficLight/internal/mariadb"
	"github.com/pokemaplx/TrafficLight/internal/pogo"
	"github.com/pokemaplx/TrafficLight/internal/printout"
	"github.com/pokemaplx/TrafficLight/internal/receiver"
	"github.com/pokemaplx/TrafficLight/internal/web"
)

func main() {
	command := "run"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	var err error
	switch command {
	case "run":
		err = run()
	case "healthcheck":
		err = healthcheck()
	default:
		err = fmt.Errorf("unknown command %q, there is only \"run\" and \"healthcheck\"", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("config.toml")
	if err != nil {
		return fmt.Errorf("Config error: %w", err)
	}
	// loads the protos now instead of on the first request. Reading them leaves a lot behind
	// that is never needed again, which goes back to the system right away
	pogo.Default()
	debug.FreeOSMemory()

	forwarder := forward.New(cfg.ForwardURL, cfg.ForwardToken)
	defer forwarder.Close()
	traffic := &receiver.Receiver{}
	if forwarder.Enabled() {
		traffic.Forward = forwarder.Send
	}

	var servers []*http.Server
	listen := func(what, host string, port int, handler http.Handler) error {
		address := net.JoinHostPort(host, strconv.Itoa(port))
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("Couldn't start the %s on %s: %w", what, address, err)
		}
		server := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
		servers = append(servers, server)
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(os.Stderr, "The %s stopped: %v\n", what, err)
				os.Exit(1)
			}
		}()
		return nil
	}

	switch cfg.Output {
	case "print":
		traffic.Sink = printout.New(os.Stdout).Add
		if err := listen("receiver", cfg.Host, cfg.Port, traffic); err != nil {
			return err
		}
		fmt.Printf("Send your traffic to %s\n", web.ReceiverURL(cfg.Host, cfg.Port))

	case "web":
		limit := int64(cfg.WebMaxMemoryMB) << 20
		limitMemory(limit)
		options := web.Options{
			Host:        cfg.WebHost,
			Password:    cfg.WebPassword,
			MaxRecords:  cfg.WebMaxRecords,
			MaxBytes:    limit,
			ReceiverURL: web.ReceiverURL(cfg.Host, cfg.Port),
		}
		if cfg.DatabaseURL != "" {
			archive, err := mariadb.Open(cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("Couldn't use the database for saved requests: %w", err)
			}
			defer archive.Close()
			options.Archive = archive
		}

		ui := web.New(options)
		restored, err := ui.Restore()
		if err != nil {
			return fmt.Errorf("Couldn't read the saved requests from the database: %w", err)
		}
		traffic.Sink = ui.Add

		if err := listen("web UI", cfg.WebHost, cfg.WebPort, ui); err != nil {
			return err
		}
		if err := listen("receiver", cfg.Host, cfg.Port, traffic); err != nil {
			return err
		}

		url := web.URL(cfg.WebHost, cfg.WebPort)
		fmt.Printf("Traffic Light is running at %s\n", url)
		fmt.Printf("Send your traffic to %s\n", web.ReceiverURL(cfg.Host, cfg.Port))
		if ui.AuthEnabled() {
			fmt.Println("The web UI asks for your password")
		}
		if cfg.DatabaseURL != "" {
			fmt.Printf("Saved requests are kept in the database, %d were there already\n", restored)
		}
		if cfg.WebMaxMemoryMB > 0 {
			fmt.Printf("The log keeps up to %d requests in at most %d MB\n", cfg.WebMaxRecords, cfg.WebMaxMemoryMB)
		}
		if cfg.WebOpenBrowser {
			web.OpenBrowser(url)
		}
	}
	if forwarder.Enabled() {
		fmt.Printf("Forwarding everything to %s\n", cfg.ForwardURL)
	}
	if cfg.ProtosURL != "" || cfg.MethodsURL != "" {
		// in the background: Traffic Light takes traffic right away, with the protos it was
		// built with, and switches to newer ones as soon as it has them
		updater := &pogo.Updater{
			Protos:  cfg.ProtosURL,
			Methods: cfg.MethodsURL,
			Logf:    func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
		}
		watching, stopWatching := context.WithCancel(context.Background())
		defer stopWatching()
		go updater.Run(watching, time.Duration(cfg.ProtosUpdateHours)*time.Hour)

		if cfg.ProtosUpdateHours > 0 {
			fmt.Printf("New protos are looked for every %d hours\n", cfg.ProtosUpdateHours)
		} else {
			fmt.Println("New protos are looked for now, and not again")
		}
	}
	if cfg.DebugAddr != "" {
		// what Go itself knows about where memory and time go:
		// go tool pprof http://<debug_addr>/debug/pprof/heap
		profiler := http.NewServeMux()
		profiler.HandleFunc("/debug/pprof/", pprof.Index)
		profiler.HandleFunc("/debug/pprof/profile", pprof.Profile)
		profiler.HandleFunc("/debug/pprof/trace", pprof.Trace)
		go func() {
			if err := http.ListenAndServe(cfg.DebugAddr, profiler); err != nil {
				fmt.Fprintf(os.Stderr, "The profiler stopped: %v\n", err)
			}
		}()
		fmt.Printf("The profiler is at http://%s/debug/pprof/\n", cfg.DebugAddr)
	}
	fmt.Println("Press Ctrl+C to quit")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	for _, server := range servers {
		// browsers keep their websocket open forever, so nothing is waited for
		_ = server.Close()
	}
	return nil
}

// limitMemory keeps Traffic Light close to what its log weighs. Left alone, Go lets the heap
// grow to twice of what is in use before it cleans up, which would be twice the log. The log
// is plain bytes that cost next to nothing to look past, so cleaning up ten times as often is
// cheap. GOGC and GOMEMLIMIT override this
func limitMemory(logLimit int64) {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(10)
	}
	if logLimit > 0 && os.Getenv("GOMEMLIMIT") == "" {
		// a safety net well above what is expected, for when a lot is asked for at once.
		// Running close to it would have the runtime fight for every megabyte
		debug.SetMemoryLimit(logLimit + logLimit/2 + 128<<20)
	}
}

// healthcheck posts an empty list to the receiver, which takes it without adding a record
func healthcheck() error {
	cfg, err := config.Load("config.toml")
	if err != nil {
		return err
	}
	host := cfg.Host
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}

	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Post("http://"+net.JoinHostPort(host, strconv.Itoa(cfg.Port))+"/", "application/json", strings.NewReader("[]"))
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the receiver answered %d", response.StatusCode)
	}
	return nil
}
