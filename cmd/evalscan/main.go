// evalscan checks captured local synthetic evaluation logs without echoing them.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/enjoydarts/cycrio/internal/evaluation"
)

func main() {
	if err := scan(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type byteCounter struct{ n int64 }

func (c *byteCounter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }
func scan(args []string, input io.Reader, output io.Writer) error {
	invalid := errors.New("synthetic log evaluation unavailable")
	flags := flag.NewFlagSet("evalscan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("probes", "", "synthetic probe manifest")
	scope := flags.String("scope", "", "evaluation_worker or docker_services")
	allowEmpty := flags.Bool("allow-empty", false, "disclose an empty successful Docker log capture")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" || (*scope != "evaluation_worker" && *scope != "docker_services") || input == nil || output == nil {
		return invalid
	}
	if *allowEmpty && *scope != "docker_services" {
		return invalid
	}
	probes, err := evaluation.ReadProbes(*path)
	if err != nil {
		return invalid
	}
	defer func() {
		for _, p := range probes {
			clear(p.Value)
		}
	}()
	count := &byteCounter{}
	hits, err := evaluation.Scan(io.TeeReader(input, count), probes)
	if err != nil || len(hits) > 0 || (count.n == 0 && !*allowEmpty) {
		return errors.New("synthetic log privacy check failed")
	}
	report := struct {
		Version int    `json:"version"`
		Mode    string `json:"mode"`
		Scope   string `json:"scope"`
		Bytes   int64  `json:"bytes"`
		Empty   bool   `json:"empty"`
		Passed  bool   `json:"passed"`
	}{1, "synthetic", *scope, count.n, count.n == 0, true}
	if json.NewEncoder(output).Encode(report) != nil {
		return invalid
	}
	return nil
}
