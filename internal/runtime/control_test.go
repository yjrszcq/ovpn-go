package runtime

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

// Hold the server immediately after its success response reaches the caller.
// A caller can begin its next request before that Write returns to the handler.
type delayedApplyAck struct {
	net.Conn
	written chan struct{}
	release chan struct{}
}

func (connection delayedApplyAck) Write(value []byte) (int, error) {
	n, err := connection.Conn.Write(value)
	if string(value) == "OK\n" {
		close(connection.written)
		<-connection.release
	}
	return n, err
}

func TestControlAcceptsNextApplyAfterSuccessAcknowledgment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server := &controlServer{requests: make(chan *applyControlRequest), busy: make(chan struct{}, 1)}
	firstServer, first := net.Pipe()
	defer first.Close()
	ack := delayedApplyAck{Conn: firstServer, written: make(chan struct{}), release: make(chan struct{})}
	defer close(ack.release)
	go server.handle(ctx, ack)
	_, _ = first.Write([]byte("APPLY\n"))
	var request *applyControlRequest
	select {
	case request = <-server.requests:
	case <-ctx.Done():
		t.Fatal("supervisor did not accept the first apply")
	}
	request.ready <- nil
	reader := bufio.NewReader(first)
	if line, err := reader.ReadString('\n'); err != nil || line != "READY\n" {
		t.Fatalf("ready: %q, %v", line, err)
	}
	_, _ = first.Write([]byte("RESUME\n"))
	select {
	case <-request.resume:
	case <-ctx.Done():
		t.Fatal("supervisor did not receive the resume command")
	}
	request.done <- nil
	if line, err := reader.ReadString('\n'); err != nil || line != "OK\n" {
		t.Fatalf("resume: %q, %v", line, err)
	}
	<-ack.written

	secondServer, second := net.Pipe()
	defer second.Close()
	go server.handle(ctx, secondServer)
	_, _ = second.Write([]byte("APPLY\n"))
	select {
	case next := <-server.requests:
		next.ready <- nil
		if line, err := bufio.NewReader(second).ReadString('\n'); err != nil || line != "READY\n" {
			t.Fatalf("next ready: %q, %v", line, err)
		}
	case <-ctx.Done():
		t.Fatal("successful apply still blocks the next request after acknowledgment")
	}
}
