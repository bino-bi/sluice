// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

func TestSyslogSink_DeliversRFC5424OverTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		// RFC 6587 octet counting: "LEN SP MSG".
		lenStr, err := r.ReadString(' ')
		if err != nil {
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(lenStr))
		if err != nil {
			return
		}
		buf := make([]byte, n)
		if _, err := readFull(r, buf); err != nil {
			return
		}
		got <- string(buf)
	}()

	sink, err := audit.NewSyslogSink(audit.SyslogOptions{
		Network:  "tcp",
		Address:  ln.Addr().String(),
		Facility: "local3",
		Tag:      "sluice-test",
		Hostname: "host-a",
		Logger:   testLogger(t),
	})
	if err != nil {
		t.Fatalf("NewSyslogSink: %v", err)
	}
	defer func() { _ = sink.Close(context.Background()) }()

	rec := &audit.Record{EventType: audit.EventQuery, Decision: audit.DecisionAllow, Hash: "abc"}
	if err := sink.Record(context.Background(), rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	select {
	case msg := <-got:
		// local3 (19*8) + info (6) = 158.
		if !strings.HasPrefix(msg, "<158>1 ") {
			t.Fatalf("message PRI/version wrong: %q", msg)
		}
		if !strings.Contains(msg, " host-a sluice-test ") {
			t.Fatalf("hostname/tag missing: %q", msg)
		}
		if !strings.Contains(msg, `"hash":"abc"`) {
			t.Fatalf("canonical JSON body missing: %q", msg)
		}
		if strings.HasSuffix(msg, "\n") {
			t.Fatalf("stream message must not carry a trailing newline: %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no syslog message received")
	}
}

func TestSyslogSink_DeliveryFailureIsSwallowed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	sink, err := audit.NewSyslogSink(audit.SyslogOptions{
		Network: "tcp",
		Address: ln.Addr().String(),
		Logger:  testLogger(t),
	})
	if err != nil {
		t.Fatalf("NewSyslogSink: %v", err)
	}
	// Kill the daemon: writes (and the redial) must fail from here on.
	_ = ln.Close()

	rec := &audit.Record{EventType: audit.EventQuery, Decision: audit.DecisionAllow}
	for range 3 {
		if err := sink.Record(context.Background(), rec); err != nil {
			t.Fatalf("delivery failure must not surface to the dispatcher: %v", err)
		}
	}

	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sink.Record(context.Background(), rec); !errors.Is(err, audit.ErrClosed) {
		t.Fatalf("Record after Close = %v, want ErrClosed", err)
	}
}

func TestSyslogSink_UnreachableFailsConstruction(t *testing.T) {
	if _, err := audit.NewSyslogSink(audit.SyslogOptions{
		Network: "tcp",
		Address: "127.0.0.1:1", // nothing listens on port 1
		Logger:  testLogger(t),
	}); err == nil {
		t.Fatal("unreachable syslog daemon must fail startup")
	}
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
