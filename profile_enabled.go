//go:build profile

package main

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
)

func startProfiler() {
	if os.Getenv("ULTRA_PROFILE") == "" {
		return
	}
	go func() {
		slog.Info("Serving pprof at localhost:6060")
		if err := http.ListenAndServe("localhost:6060", nil); err != nil {
			slog.Error("Failed to pprof listen", "error", err)
		}
	}()
}
