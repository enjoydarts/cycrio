package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

var ErrProbes = errors.New("synthetic evaluation probes unavailable")

type storedProbe struct {
	ID    string `json:"id"`
	Value []byte `json:"value"`
}
type probeManifest struct {
	Version int           `json:"version"`
	Probes  []storedProbe `json:"probes"`
}

// WriteProbes writes synthetic markers only. It never replaces an existing file.
func WriteProbes(path string, probes []Probe) error {
	if _, err := Scan(bytes.NewReader(nil), probes); err != nil {
		return ErrProbes
	}
	manifest := probeManifest{Version: 1}
	for _, probe := range probes {
		manifest.Probes = append(manifest.Probes, storedProbe{probe.ID, probe.Value})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return ErrProbes
	}
	defer clear(raw)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrProbes
	}
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return ErrProbes
	}
	return nil
}

func ReadProbes(path string) ([]Probe, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrProbes
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 131073))
	if err != nil || len(raw) > 131072 {
		return nil, ErrProbes
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest probeManifest
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.Version != 1 {
		return nil, ErrProbes
	}
	probes := make([]Probe, 0, len(manifest.Probes))
	for _, probe := range manifest.Probes {
		probes = append(probes, Probe{ID: probe.ID, Value: probe.Value})
	}
	if _, err = Scan(bytes.NewReader(nil), probes); err != nil {
		return nil, ErrProbes
	}
	return probes, nil
}
