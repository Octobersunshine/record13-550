package geohash

import (
	"bytes"
	"fmt"
	"math"
)

const (
	base32          = "0123456789bcdefghjkmnpqrstuvwxyz"
	DefaultPrecision = 6
)

var bits = []int{16, 8, 4, 2, 1}

func Encode(lat, lon float64, precision int) (string, error) {
	if precision <= 0 {
		precision = DefaultPrecision
	}
	if lat < -90 || lat > 90 {
		return "", fmt.Errorf("latitude out of range: %f", lat)
	}
	if lon < -180 || lon > 180 {
		return "", fmt.Errorf("longitude out of range: %f", lon)
	}

	var buf bytes.Buffer
	var latRange = [2]float64{-90, 90}
	var lonRange = [2]float64{-180, 180}

	bit := 0
	ch := 0
	isEven := true

	for buf.Len() < precision {
		var mid float64
		if isEven {
			mid = (lonRange[0] + lonRange[1]) / 2
			if lon >= mid {
				ch |= bits[bit]
				lonRange[0] = mid
			} else {
				lonRange[1] = mid
			}
		} else {
			mid = (latRange[0] + latRange[1]) / 2
			if lat >= mid {
				ch |= bits[bit]
				latRange[0] = mid
			} else {
				latRange[1] = mid
			}
		}

		isEven = !isEven
		if bit < 4 {
			bit++
		} else {
			buf.WriteByte(base32[ch])
			bit = 0
			ch = 0
		}
	}

	return buf.String(), nil
}

func Decode(hash string) (lat, lon float64, err error) {
	if len(hash) == 0 {
		return 0, 0, fmt.Errorf("empty geohash")
	}

	var latRange = [2]float64{-90, 90}
	var lonRange = [2]float64{-180, 180}
	isEven := true

	for i := 0; i < len(hash); i++ {
		c := hash[i]
		idx := bytes.IndexByte([]byte(base32), c)
		if idx < 0 {
			return 0, 0, fmt.Errorf("invalid geohash character: %c", c)
		}

		for j := 4; j >= 0; j-- {
			bit := (idx >> uint(j)) & 1
			if isEven {
				mid := (lonRange[0] + lonRange[1]) / 2
				if bit == 1 {
					lonRange[0] = mid
				} else {
					lonRange[1] = mid
				}
			} else {
				mid := (latRange[0] + latRange[1]) / 2
				if bit == 1 {
					latRange[0] = mid
				} else {
					latRange[1] = mid
				}
			}
			isEven = !isEven
		}
	}

	lat = (latRange[0] + latRange[1]) / 2
	lon = (lonRange[0] + lonRange[1]) / 2
	return lat, lon, nil
}

func ExpandNeighbors(hash string) []string {
	if len(hash) == 0 {
		return []string{}
	}
	lat, lon, err := Decode(hash)
	if err != nil {
		return []string{hash}
	}

	prec := len(hash)
	latStep := calcLatStep(prec)
	lonStep := calcLonStep(prec)

	neighbors := make([]string, 0, 9)
	for dLat := -1; dLat <= 1; dLat++ {
		for dLon := -1; dLon <= 1; dLon++ {
			nLat := lat + float64(dLat)*latStep
			nLon := lon + float64(dLon)*lonStep
			if nLat >= -90 && nLat <= 90 && nLon >= -180 && nLon <= 180 {
				if h, err := Encode(nLat, nLon, prec); err == nil {
					neighbors = append(neighbors, h)
				}
			}
		}
	}
	return neighbors
}

func calcLatStep(precision int) float64 {
	return 180.0 / math.Pow(2, float64(precision*5/2))
}

func calcLonStep(precision int) float64 {
	return 360.0 / math.Pow(2, float64(precision*5/2+1))
}
