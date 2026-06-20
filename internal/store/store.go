package store

import (
	"math"
	"sort"
	"sync"
	"time"

	"orderheat/internal/geohash"
)

type AreaHeat struct {
	Geohash string  `json:"geohash"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Count   int64   `json:"count"`
	Score   float64 `json:"score"`
}

type PaginatedResult struct {
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Areas    []AreaHeat `json:"areas"`
}

type DailyAreaRecord struct {
	Date     string  `json:"date"`
	Geohash  string  `json:"geohash"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	OrderID  string  `json:"order_id"`
	DriverID string  `json:"driver_id"`
}

type AcceptRecord struct {
	DriverID string
	OrderID  string
	Lat      float64
	Lon      float64
	Accuracy float64
	Time     time.Time
}

type weightedRecord struct {
	record  AcceptRecord
	weight  float64
}

type HeatStore struct {
	mu        sync.RWMutex
	records   map[string][]weightedRecord
	decayHalf time.Duration
	precision int
}

func NewHeatStore() *HeatStore {
	return &HeatStore{
		records:   make(map[string][]weightedRecord),
		decayHalf: 30 * time.Minute,
		precision: geohash.DefaultPrecision,
	}
}

func (s *HeatStore) AddAccept(r AcceptRecord) error {
	sLat, sLon, err := geohash.SnapToGrid(r.Lat, r.Lon, s.precision)
	if err != nil {
		return err
	}
	r.Lat = sLat
	r.Lon = sLon

	primaryHash, err := geohash.Encode(sLat, sLon, s.precision)
	if err != nil {
		return err
	}

	neighbors := geohash.ExpandNeighbors(primaryHash)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, nh := range neighbors {
		w := s.computeNeighborWeight(sLat, sLon, nh)
		if w < 0.01 {
			continue
		}
		s.records[nh] = append(s.records[nh], weightedRecord{
			record: r,
			weight: w,
		})
	}

	return nil
}

func (s *HeatStore) computeNeighborWeight(lat, lon float64, cellHash string) float64 {
	bbox, err := geohash.DecodeBounds(cellHash)
	if err != nil {
		return 0
	}

	cLat := (bbox.MinLat + bbox.MaxLat) / 2
	cLon := (bbox.MinLon + bbox.MaxLon) / 2

	dist := haversineKm(lat, lon, cLat, cLon)

	halfCellKm := haversineKm(bbox.MinLat, bbox.MinLon, cLat, cLon)
	if halfCellKm == 0 {
		return 1.0
	}

	sigma := halfCellKm * 0.8

	w := math.Exp(-dist*dist / (2 * sigma * sigma))

	return w
}

func (s *HeatStore) GetHeats(centerLat, centerLon float64, radiusKm float64, limit int) []AreaHeat {
	s.mu.RLock()
	defer s.mu.RUnlock()

	centerHash, _ := geohash.Encode(centerLat, centerLon, s.precision)
	expandRadius := int(math.Ceil(radiusKm / 1.2))
	var hashesToCheck []string
	if expandRadius <= 1 {
		hashesToCheck = geohash.ExpandNeighbors(centerHash)
	} else {
		hashesToCheck = s.expandArea(centerHash, expandRadius)
	}

	now := time.Now()
	results := make([]AreaHeat, 0, len(hashesToCheck))

	for _, hash := range hashesToCheck {
		wRecords, ok := s.records[hash]
		if !ok || len(wRecords) == 0 {
			continue
		}

		lat, lon, err := geohash.Decode(hash)
		if err != nil {
			continue
		}

		dist := haversineKm(centerLat, centerLon, lat, lon)
		if dist > radiusKm+1.0 {
			continue
		}

		var score float64
		var count int64
		seen := make(map[string]struct{})
		for _, wr := range wRecords {
			elapsed := now.Sub(wr.record.Time)
			decay := math.Pow(0.5, float64(elapsed)/float64(s.decayHalf))
			score += wr.weight * decay
			key := wr.record.OrderID
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				count++
			}
		}

		results = append(results, AreaHeat{
			Geohash: hash,
			Lat:     lat,
			Lon:     lon,
			Count:   count,
			Score:   math.Round(score*100) / 100,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

func (s *HeatStore) GetAllHeats(limit int) []AreaHeat {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	results := make([]AreaHeat, 0, len(s.records))

	for hash, wRecords := range s.records {
		if len(wRecords) == 0 {
			continue
		}

		lat, lon, err := geohash.Decode(hash)
		if err != nil {
			continue
		}

		var score float64
		var count int64
		seen := make(map[string]struct{})
		for _, wr := range wRecords {
			elapsed := now.Sub(wr.record.Time)
			decay := math.Pow(0.5, float64(elapsed)/float64(s.decayHalf))
			score += wr.weight * decay
			key := wr.record.OrderID
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				count++
			}
		}

		results = append(results, AreaHeat{
			Geohash: hash,
			Lat:     lat,
			Lon:     lon,
			Count:   count,
			Score:   math.Round(score*100) / 100,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}

func (s *HeatStore) PurgeExpired(olderThan time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan)
	purged := 0

	for hash, wRecords := range s.records {
		kept := wRecords[:0]
		for _, wr := range wRecords {
			if wr.record.Time.After(cutoff) {
				kept = append(kept, wr)
			} else {
				purged++
			}
		}
		if len(kept) == 0 {
			delete(s.records, hash)
		} else {
			s.records[hash] = kept
		}
	}

	return purged
}

func (s *HeatStore) GetHeatsPaginated(startDate, endDate time.Time, page, pageSize int) PaginatedResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	allAreas := s.computeHeatsInRange(startDate, endDate)

	total := len(allAreas)
	start := (page - 1) * pageSize
	end := start + pageSize

	if start >= total {
		return PaginatedResult{
			Total:    total,
			Page:     page,
			PageSize: pageSize,
			Areas:    []AreaHeat{},
		}
	}
	if end > total {
		end = total
	}

	return PaginatedResult{
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Areas:    allAreas[start:end],
	}
}

func (s *HeatStore) GetDailyDetail(startDate, endDate time.Time) []DailyAreaRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var records []DailyAreaRecord
	seen := make(map[string]struct{})

	for hash, wRecords := range s.records {
		lat, lon, err := geohash.Decode(hash)
		if err != nil {
			continue
		}

		for _, wr := range wRecords {
			if wr.weight < 0.99 {
				continue
			}

			t := wr.record.Time
			if (!startDate.IsZero() && t.Before(startDate)) ||
				(!endDate.IsZero() && t.After(endDate)) {
				continue
			}

			key := wr.record.OrderID + "|" + hash
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			records = append(records, DailyAreaRecord{
				Date:     t.Format("2006-01-02"),
				Geohash:  hash,
				Lat:      lat,
				Lon:      lon,
				OrderID:  wr.record.OrderID,
				DriverID: wr.record.DriverID,
			})
		}
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].Date != records[j].Date {
			return records[i].Date < records[j].Date
		}
		return records[i].Geohash < records[j].Geohash
	})

	return records
}

func (s *HeatStore) computeHeatsInRange(startDate, endDate time.Time) []AreaHeat {
	now := time.Now()
	results := make([]AreaHeat, 0, len(s.records))

	for hash, wRecords := range s.records {
		if len(wRecords) == 0 {
			continue
		}

		lat, lon, err := geohash.Decode(hash)
		if err != nil {
			continue
		}

		var score float64
		var count int64
		seen := make(map[string]struct{})

		for _, wr := range wRecords {
			t := wr.record.Time
			if (!startDate.IsZero() && t.Before(startDate)) ||
				(!endDate.IsZero() && t.After(endDate)) {
				continue
			}

			elapsed := now.Sub(t)
			decay := math.Pow(0.5, float64(elapsed)/float64(s.decayHalf))
			score += wr.weight * decay

			key := wr.record.OrderID
			if _, exists := seen[key]; !exists && wr.weight >= 0.99 {
				seen[key] = struct{}{}
				count++
			}
		}

		if count == 0 && score < 0.01 {
			continue
		}

		results = append(results, AreaHeat{
			Geohash: hash,
			Lat:     lat,
			Lon:     lon,
			Count:   count,
			Score:   math.Round(score*100) / 100,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

func (s *HeatStore) expandArea(centerHash string, levels int) []string {
	lat, lon, err := geohash.Decode(centerHash)
	if err != nil {
		return []string{centerHash}
	}

	latStep := geohash.CalcLatStep(s.precision)
	lonStep := geohash.CalcLonStep(s.precision)

	set := make(map[string]struct{})
	for dLat := -levels; dLat <= levels; dLat++ {
		for dLon := -levels; dLon <= levels; dLon++ {
			nLat := lat + float64(dLat)*latStep
			nLon := lon + float64(dLon)*lonStep
			if nLat >= -90 && nLat <= 90 && nLon >= -180 && nLon <= 180 {
				if h, err := geohash.Encode(nLat, nLon, s.precision); err == nil {
					set[h] = struct{}{}
				}
			}
		}
	}

	result := make([]string, 0, len(set))
	for h := range set {
		result = append(result, h)
	}
	return result
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0

	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0

	lat1Rad := lat1 * math.Pi / 180.0
	lat2Rad := lat2 * math.Pi / 180.0

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}
