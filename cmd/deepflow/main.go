// Command deepflow runs the DeepFlow learning environment on localhost.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"deepflow/internal/api"
	"deepflow/internal/orchestrator"
	"deepflow/internal/provider"
	"deepflow/internal/session"
	"deepflow/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func dataDir() (string, error) {
	if d := os.Getenv("DEEPFLOW_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deepflow"), nil
}

func run() error {
	dir, err := dataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dir, "deepflow.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	p, err := provider.FromEnv()
	if err != nil {
		return err
	}
	if m, ok := p.(*provider.Mock); ok {
		m.SetStreamStepDelay(15 * time.Millisecond)
	}
	policy := orchestrator.DefaultPolicy()
	if v := os.Getenv("DEEPFLOW_ADAPT_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("DEEPFLOW_ADAPT_WINDOW: %w", err)
		}
		policy.AdaptWindow = d
	}
	mgr := session.NewManager(session.Config{Store: st, Provider: p, Policy: policy})
	defer mgr.Shutdown()

	port := os.Getenv("DEEPFLOW_PORT")
	if port == "" {
		port = "7878"
	}
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: api.New(mgr, st, p), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	fmt.Printf("DeepFlow is running at http://127.0.0.1:%s (provider: %s, data: %s)\n", port, p.Name(), dir)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		stop() // restore default signals, so a second Ctrl-C quits at once
		fmt.Println("\nShutting down…")
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// Streams never finish on their own, so a timeout here is expected.
		_ = srv.Shutdown(shutdown)
		_ = srv.Close()
	}
	return nil
}
