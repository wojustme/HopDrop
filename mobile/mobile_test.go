package mobile

import (
	"errors"
	"strings"
	"testing"

	"github.com/xurenhe/hopdrop/core/protocol"
)

type recordingSink struct {
	written  int
	closed   bool
	aborted  bool
	writeErr error
	closeErr error
}

func (*recordingSink) OpenWrite(string) (string, error) { return "handle", nil }
func (s *recordingSink) Write(_ string, data []byte) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.written += len(data)
	return nil
}
func (s *recordingSink) Close(string) error { s.closed = true; return s.closeErr }
func (s *recordingSink) Abort(string) error { s.aborted = true; return nil }

func TestSinkAdapterCommitsExactContent(t *testing.T) {
	host := &recordingSink{}
	adapter := &sinkAdapter{sink: host}
	meta := protocol.FileMeta{ID: "1", Name: "file", RelPath: "file", Size: 4}
	if err := adapter.Create(meta, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if host.written != 4 || !host.closed || host.aborted {
		t.Fatalf("unexpected sink state: %#v", host)
	}
}

func TestSinkAdapterAbortsIncompleteContent(t *testing.T) {
	host := &recordingSink{}
	adapter := &sinkAdapter{sink: host}
	meta := protocol.FileMeta{ID: "1", Name: "file", RelPath: "file", Size: 10}
	if err := adapter.Create(meta, strings.NewReader("short")); err == nil {
		t.Fatal("short content was accepted")
	}
	if host.closed || !host.aborted {
		t.Fatalf("unexpected sink state: %#v", host)
	}
}

func TestSinkAdapterAbortsWriteFailure(t *testing.T) {
	host := &recordingSink{writeErr: errors.New("write failed")}
	adapter := &sinkAdapter{sink: host}
	meta := protocol.FileMeta{ID: "1", Name: "file", RelPath: "file", Size: 4}
	if err := adapter.Create(meta, strings.NewReader("data")); err == nil {
		t.Fatal("write failure was ignored")
	}
	if host.closed || !host.aborted {
		t.Fatalf("unexpected sink state: %#v", host)
	}
}

func TestSinkAdapterAbortsCloseFailure(t *testing.T) {
	host := &recordingSink{closeErr: errors.New("close failed")}
	adapter := &sinkAdapter{sink: host}
	meta := protocol.FileMeta{ID: "1", Name: "file", RelPath: "file", Size: 4}
	if err := adapter.Create(meta, strings.NewReader("data")); err == nil {
		t.Fatal("close failure was ignored")
	}
	if !host.closed || !host.aborted {
		t.Fatalf("unexpected sink state: %#v", host)
	}
}
