package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"audioservice/internal/httpapi"
	"audioservice/internal/media"
	"audioservice/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	usage := errors.New("usage: audioservice migrate | user create --name NAME | serve [flags]")
	if len(args) == 0 {
		return usage
	}
	switch args[0] {
	case "migrate":
		if len(args) != 1 {
			return usage
		}
		return withStore(ctx, func(database *store.Store) error {
			return database.Migrate(ctx)
		})
	case "user":
		if len(args) < 2 || args[1] != "create" {
			return usage
		}
		flags := flag.NewFlagSet("user create", flag.ContinueOnError)
		name := flags.String("name", "", "display name")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *name == "" {
			return usage
		}
		return withStore(ctx, func(database *store.Store) error {
			key, err := database.CreateUser(ctx, *name)
			if err == nil {
				fmt.Println(key)
			}
			return err
		})
	case "serve":
		return serve(ctx, args[1:])
	default:
		return usage
	}
}

func withStore(ctx context.Context, operation func(*store.Store) error) error {
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	database, err := store.Open(connectCtx, os.Getenv("AUDIOSERVICE_DATABASE_URL"))
	cancel()
	if err != nil {
		return err
	}
	defer database.Close()
	return operation(database)
}

func serve(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listener")
	dataDir := flags.String("data-dir", "data", "directory for media files")
	cert := flags.String("tls-cert", "", "TLS certificate chain PEM")
	key := flags.String("tls-key", "", "TLS private key PEM")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*cert == "") != (*key == "") {
		return errors.New("use TLS certificate and key together")
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	if *cert == "" && !listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		return errors.New("TLS is required for a non-loopback listener")
	}
	return withStore(ctx, func(database *store.Store) error {
		files, err := media.Open(*dataDir)
		if err != nil {
			return err
		}
		server := &http.Server{
			Handler:           httpapi.New(database, files),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				server.Close()
			}
		}()
		if *cert != "" {
			log.Printf("HTTPS listening on %s", listener.Addr())
			err = server.ServeTLS(listener, *cert, *key)
		} else {
			log.Printf("HTTP listening on %s", listener.Addr())
			err = server.Serve(listener)
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
}
