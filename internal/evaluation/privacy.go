// Package evaluation provides metadata reports for isolated synthetic evaluation.
package evaluation

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
)

var ErrScan = errors.New("evaluation scan unavailable")
var safeID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type Probe struct {
	ID    string `json:"id"`
	Value []byte `json:"-"`
}

// Scan returns probe IDs only. It tests literal, hex and standalone base64
// representations, including markers split across reads, using bounded memory.
// It does not prove absence of every possible encoding or transformation.
func Scan(reader io.Reader, probes []Probe) ([]string, error) {
	if reader == nil || len(probes) == 0 || len(probes) > 16 {
		return nil, ErrScan
	}
	patterns := make([][][]byte, len(probes))
	overlap := 0
	seen := map[string]bool{}
	for i, p := range probes {
		if !safeID.MatchString(p.ID) || seen[p.ID] || len(p.Value) < 4 || len(p.Value) > 4096 {
			return nil, ErrScan
		}
		seen[p.ID] = true
		patterns[i] = [][]byte{bytes.Clone(p.Value), []byte(hex.EncodeToString(p.Value)), []byte(base64.StdEncoding.EncodeToString(p.Value)), []byte(base64.RawStdEncoding.EncodeToString(p.Value))}
		for _, pattern := range patterns[i] {
			overlap = max(overlap, len(pattern)-1)
		}
	}
	found := make([]bool, len(probes))
	buffer := make([]byte, 32768)
	tail := []byte{}
	emptyReads := 0
	for {
		n, err := reader.Read(buffer)
		if n < 0 || n > len(buffer) {
			return nil, ErrScan
		}
		if n > 0 {
			emptyReads = 0
			window := append(tail, buffer[:n]...)
			for i, list := range patterns {
				for _, pattern := range list {
					if bytes.Contains(window, pattern) {
						found[i] = true
						break
					}
				}
			}
			keep := min(overlap, len(window))
			tail = bytes.Clone(window[len(window)-keep:])
		} else {
			emptyReads++
			if emptyReads > 100 {
				return nil, ErrScan
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrScan
		}
	}
	hits := []string{}
	for i, p := range probes {
		if found[i] {
			hits = append(hits, p.ID)
		}
	}
	return hits, nil
}
