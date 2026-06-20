package handler

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
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

func (h *Handler) HandleHeatExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	startDate, endDate, err := parseDateRange(q.Get("start_date"), q.Get("end_date"))
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	pageSize := 20
	if ps, err := strconv.Atoi(q.Get("page_size")); err == nil && ps > 0 && ps <= 500 {
		pageSize = ps
	}

	result := h.store.GetHeatsPaginated(startDate, endDate, page, pageSize)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) HandleDailyDetailCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	startDate, endDate, err := parseDateRange(q.Get("start_date"), q.Get("end_date"))
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	records := h.store.GetDailyDetail(startDate, endDate)

	filename := fmt.Sprintf("order_detail_%s.csv", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{"Date", "Geohash", "Latitude", "Longitude", "OrderID", "DriverID"}
	if err := cw.Write(header); err != nil {
		writeError(w, "failed to write csv header", http.StatusInternalServerError)
		return
	}

	for _, rec := range records {
		row := []string{
			rec.Date,
			rec.Geohash,
			strconv.FormatFloat(rec.Lat, 'f', 6, 64),
			strconv.FormatFloat(rec.Lon, 'f', 6, 64),
			rec.OrderID,
			rec.DriverID,
		}
		if err := cw.Write(row); err != nil {
			break
		}
	}
}

func parseDateRange(startStr, endStr string) (time.Time, time.Time, error) {
	var startDate, endDate time.Time

	if startStr != "" {
		t, err := time.ParseInLocation("2006-01-02", startStr, time.Local)
		if err != nil {
			return startDate, endDate, fmt.Errorf("invalid start_date format, expected YYYY-MM-DD: %s", startStr)
		}
		startDate = t
	}

	if endStr != "" {
		t, err := time.ParseInLocation("2006-01-02", endStr, time.Local)
		if err != nil {
			return startDate, endDate, fmt.Errorf("invalid end_date format, expected YYYY-MM-DD: %s", endStr)
		}
		endDate = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	}

	if !startDate.IsZero() && !endDate.IsZero() && startDate.After(endDate) {
		return startDate, endDate, fmt.Errorf("start_date must be before end_date")
	}

	return startDate, endDate, nil
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
}
