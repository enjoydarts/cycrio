package fastmail

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

func TestWireGuardTreatsLiteralContentAsOpaque(t *testing.T) {
	// Neither CR, quotes nor a fake oversized literal header in literal bytes
	// may affect the guard's framing or declaration tracking.
	payload := "a\rb\n\"{2147483648}\rX"
	input := fmt.Sprintf("* LIST () NIL {%d}\r\n%s\r\nT1 OK listed\r\n", len(payload), payload)
	for _, fragmented := range []bool{false, true} {
		t.Run(fmt.Sprint(fragmented), func(t *testing.T) {
			client, server := net.Pipe()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				if fragmented {
					for _, b := range []byte(input) {
						if _, err := server.Write([]byte{b}); err != nil {
							return
						}
					}
				} else {
					io.WriteString(server, input)
				}
			}()
			wire := &boundedConn{Conn: client}
			got, err := io.ReadAll(wire)
			client.Close()
			waitClosed(t, done)
			if err != nil || string(got) != input || wire.exceeded.Load() {
				t.Fatal("literal content changed framing", err, string(got))
			}
		})
	}
}

func TestWireBudgetIncludesEveryByte(t *testing.T) {
	for _, size := range []int{maxResponseBytes, maxResponseBytes + 1} {
		client, server := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer server.Close()
			io.WriteString(server, strings.Repeat("x", size))
		}()
		wire := &boundedConn{Conn: client}
		n, err := io.Copy(io.Discard, wire)
		client.Close()
		waitClosed(t, done)
		if size == maxResponseBytes && (err != nil || n != maxResponseBytes) {
			t.Fatal("exact budget rejected", n, err)
		}
		if size > maxResponseBytes && (err != errResponseTooLarge || !wire.exceeded.Load()) {
			t.Fatal("over budget accepted", n, err)
		}
	}
}
