package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"orderheat/internal/handler"
	"orderheat/internal/store"
)

func main() {
	hs := store.NewHeatStore()
	h := handler.NewHandler(hs)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/driver/accept", h.HandleAccept)
	mux.HandleFunc("/api/heat", h.HandleHeat)

	go startPurgeRoutine(hs)

	addr := ":8080"
	fmt.Printf("Server starting on %s\n", addr)
	fmt.Println("POST /api/driver/accept - Submit driver order acceptance location")
	fmt.Println("GET  /api/heat             - Get area heat statistics")
	fmt.Println("  Query params: lat, lon, radius (km), limit")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func startPurgeRoutine(hs *store.HeatStore) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		purged := hs.PurgeExpired(4 * time.Hour)
		if purged > 0 {
			log.Printf("purged %d expired records", purged)
		}
	}
}
