package control

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"barely/internal/proto"
)

// TestConcurrentRequests pins that requests sent from several goroutines at
// once reach the agent as whole frames. The host process does exactly this
// (file events, trim, clock resync, shell and shutdown share one Client), and
// a frame is two writes, so without serialising them one request's length
// lands in front of another's body and the stream is unreadable from then on.
func TestConcurrentRequests(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	// A torn frame leaves both ends blocked on the pipe; the deadline turns
	// that into a failure.
	deadline := time.Now().Add(20 * time.Second)
	host.SetDeadline(deadline)
	guest.SetDeadline(deadline)

	// The agent's part: read frames and echo each request's payload back.
	go func() {
		for {
			m, err := proto.Read(guest)
			if err != nil {
				return
			}
			if err := proto.Write(guest, m); err != nil {
				return
			}
		}
	}()

	c := &Client{conn: host, pending: map[uint64]chan proto.Message{}, shellExits: map[int]chan int{}}
	go c.Run()

	const senders, each = 8, 50
	var wg sync.WaitGroup
	errs := make(chan error, senders*each)
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			// Sizes differ per sender so interleaved frames can't line up.
			want := strings.Repeat(string(rune('a'+s)), 100*(s+1))
			for i := 0; i < each; i++ {
				var got string
				if err := c.Request("echo", want, &got, 5*time.Second); err != nil {
					errs <- err
					return
				}
				if got != want {
					t.Errorf("sender %d: reply carried another request's payload", s)
					return
				}
			}
		}(s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
