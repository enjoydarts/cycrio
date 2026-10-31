package mailprocessing

import (
	"errors"
	"testing"
)

type readerLifecycle struct{ closed int }

func (c *readerLifecycle) Close() error { c.closed++; return errors.New("synthetic shutdown error") }
func TestServiceClosesReaderResourcesOnceAndPreservesCloseError(t *testing.T) {
	c := &readerLifecycle{}
	service := NewService(nil, nil, nil, nil, nil, WithReaderCloser(c))
	for range 2 {
		if e := service.Close(); e == nil {
			t.Fatal("reader shutdown error lost")
		}
	}
	if c.closed != 1 {
		t.Fatal("reader resources not closed exactly once", c.closed)
	}
	if e := NewService(nil, nil, nil, nil, nil).Close(); e != nil {
		t.Fatal("stateless service shutdown failed", e)
	}
}
