package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"orderheat/internal/store"
)

type AcceptRequest struct {
	DriverID string  `json:"driver_id"`
	OrderID  string  `json:"order_id"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Accuracy float64 `json:"accuracy"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type Handler struct {
	store *store.HeatStore
}

func NewHandler(s *store.HeatStore) *Handler {
	return &Handler{store: s}
}

func (h *Handler) HandleAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AcceptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.DriverID == "" {
		writeError(w, "driver_id is required", http.StatusBadRequest)
		return
	}
	if req.OrderID == "" {
		writeError(w, "order_id is required", http.StatusBadRequest)
		return
	}
	if req.Lat < -90 || req.Lat > 90 {
		writeError(w, "lat must be between -90 and 90", http.StatusBadRequest)
		return
	}
	if req.Lon < -180 || req.Lon > 180 {
		writeError(w, "lon must be between -180 and 180", http.StatusBadRequest)
		return
	}
	if req.Accuracy < 0 {
		req.Accuracy = 0
	}

	record := store.AcceptRecord{
		DriverID: req.DriverID,
		OrderID:  req.OrderID,
		Lat:      req.Lat,
		Lon:      req.Lon,
		Accuracy: req.Accuracy,
		Time:     time.Now(),
	}

	if err := h.store.AddAccept(record); err != nil {
		writeError(w, "failed to process: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "accept recorded",
	})
}

func (h *Handler) HandleHeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	var heats []store.AreaHeat

	latStr := q.Get("lat")
	lonStr := q.Get("lon")
	radiusStr := q.Get("radius")
	limitStr := q.Get("limit")

	limit := 0
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	if latStr != "" && lonStr != "" {
		lat, err := strconv.ParseFloat(latStr, 64)
		if err != nil || lat < -90 || lat > 90 {
			writeError(w, "invalid lat", http.StatusBadRequest)
			return
		}
		lon, err := strconv.ParseFloat(lonStr, 64)
		if err != nil || lon < -180 || lon > 180 {
			writeError(w, "invalid lon", http.StatusBadRequest)
			return
		}

		radiusKm := 5.0
		if radiusStr != "" {
			if r, err := strconv.ParseFloat(radiusStr, 64); err == nil && r > 0 {
				radiusKm = r
			}
		}

		heats = h.store.GetHeats(lat, lon, radiusKm, limit)
	} else {
		heats = h.store.GetAllHeats(limit)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total": len(heats),
		"areas": heats,
	})
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
}
