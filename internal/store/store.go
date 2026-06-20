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

type AcceptRecord struct {
	DriverID string
	OrderID  string
	Lat      float64
	Lon      float64
	Time     time.Time
}

type HeatStore struct {
	mu        sync.RWMutex
	records   map[string][]AcceptRecord
	counts    map[string]map[string]int64
	decayHalf time.Duration
	precision int
}

func NewHeatStore() *HeatStore {
	return &HeatStore{
		records:   make(map[string][]AcceptRecord),
		counts:    make(map[string]map[string]int64),
		decayHalf: 30 * time.Minute,
		precision: geohash.DefaultPrecision,
	}
}

func (s *HeatStore) AddAccept(r AcceptRecord) error {
	hash, err := geohash.Encode(r.Lat, r.Lon, s.precision)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.records[hash] = append(s.records[hash], r)
	if s.counts[hash] == nil {
		s.counts[hash] = make(map[string]int64)
	}
	s.counts[hash][r.DriverID]++
	return nil
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
		records, ok := s.records[hash]
		if !ok || len(records) == 0 {
			continue
		}

		var score float64
		for _, r := range records {
			elapsed := now.Sub(r.Time)
			decay := math.Pow(0.5, float64(elapsed)/float64(s.decayHalf))
			score += decay
		}

		lat, lon, err := geohash.Decode(hash)
		if err != nil {
			continue
		}

		dist := haversineKm(centerLat, centerLon, lat, lon)
		if dist > radiusKm {
			continue
		}

		results = append(results, AreaHeat{
			Geohash: hash,
			Lat:     lat,
			Lon:     lon,
			Count:   int64(len(records)),
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

	for hash, records := range s.records {
		if len(records) == 0 {
			continue
		}

		var score float64
		for _, r := range records {
			elapsed := now.Sub(r.Time)
			decay := math.Pow(0.5, float64(elapsed)/float64(s.decayHalf))
			score += decay
		}

		lat, lon, err := geohash.Decode(hash)
		if err != nil {
			continue
		}

		results = append(results, AreaHeat{
			Geohash: hash,
			Lat:     lat,
			Lon:     lon,
			Count:   int64(len(records)),
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

	for hash, records := range s.records {
		kept := records[:0]
		for _, r := range records {
			if r.Time.After(cutoff) {
				kept = append(kept, r)
			} else {
				purged++
			}
		}
		if len(kept) == 0 {
			delete(s.records, hash)
			delete(s.counts, hash)
		} else {
			s.records[hash] = kept
		}
	}

	return purged
}

func (s *HeatStore) expandArea(centerHash string, levels int) []string {
	lat, lon, err := geohash.Decode(centerHash)
	if err != nil {
		return []string{centerHash}
	}

	latStep := 0.0055
	lonStep := 0.0055

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
