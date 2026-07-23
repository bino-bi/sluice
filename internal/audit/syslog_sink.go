// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// SyslogSink forwards each audit record to a syslog daemon as an RFC 5424
// message carrying the canonical JSON line. It is a best-effort secondary
// sink: the file sink remains the durable, hash-chained record, so
// delivery failures are counted (sluice_audit_dropped_total{sink="syslog"})
// and logged on state transitions, never propagated to the dispatcher.
// Stream transports (tcp, unix) use RFC 6587 octet-counting framing;
// datagram transports (udp, unixgram) send bare messages.
type SyslogSink struct {
	opts SyslogOptions
	pri  int

	mu      sync.Mutex
	conn    net.Conn
	stream  bool
	failing bool
	closed  bool
}

// SyslogOptions configures NewSyslogSink. Address is required; zero values
// elsewhere pick defaults.
type SyslogOptions struct {
	// Network is udp (default), tcp, unix, or unixgram.
	Network string
	// Address is host:port (udp/tcp) or a socket path (unix/unixgram).
	Address string
	// Facility is local0..local7, daemon, auth, syslog, or user.
	// Default local0.
	Facility string
	// Tag is the RFC 5424 APP-NAME. Default "sluice".
	Tag string
	// Hostname overrides the HOSTNAME field. Default os.Hostname().
	Hostname string
	// DialTimeout bounds the initial connect and any redial. Default 5s.
	DialTimeout time.Duration
	// WriteTimeout bounds each message write. Default 5s.
	WriteTimeout time.Duration

	Clock  func() time.Time
	Logger *slog.Logger
}

// syslogSeverityInfo is the fixed severity for audit forwarding.
const syslogSeverityInfo = 6

var syslogFacilities = map[string]int{
	"kern": 0, "user": 1, "daemon": 3, "auth": 4, "syslog": 5,
	"local0": 16, "local1": 17, "local2": 18, "local3": 19,
	"local4": 20, "local5": 21, "local6": 22, "local7": 23,
}

// NewSyslogSink dials the daemon and returns the sink. An unreachable
// address fails startup — a misconfigured forwarding target must be
// visible at boot, not discovered from a silent metric.
func NewSyslogSink(opts SyslogOptions) (*SyslogSink, error) {
	if opts.Address == "" {
		return nil, fmt.Errorf("audit: syslog sink requires an address")
	}
	if opts.Network == "" {
		opts.Network = "udp"
	}
	if opts.Facility == "" {
		opts.Facility = "local0"
	}
	facility, ok := syslogFacilities[opts.Facility]
	if !ok {
		return nil, fmt.Errorf("audit: unknown syslog facility %q", opts.Facility)
	}
	if opts.Tag == "" {
		opts.Tag = "sluice"
	}
	if opts.Hostname == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = "-"
		}
		opts.Hostname = h
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 5 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 5 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	var stream bool
	switch opts.Network {
	case "tcp", "unix":
		stream = true
	case "udp", "unixgram":
	default:
		return nil, fmt.Errorf("audit: unsupported syslog network %q", opts.Network)
	}

	conn, err := net.DialTimeout(opts.Network, opts.Address, opts.DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("audit: dial syslog %s %s: %w", opts.Network, opts.Address, err)
	}

	return &SyslogSink{
		opts:   opts,
		pri:    facility*8 + syslogSeverityInfo,
		conn:   conn,
		stream: stream,
	}, nil
}

// Name implements Sink.
func (s *SyslogSink) Name() string { return "syslog" }

// Record implements Sink. Delivery failures are self-accounted; only
// ErrClosed and marshal failures surface to the dispatcher.
func (s *SyslogSink) Record(_ context.Context, r *Record) error {
	line, err := MarshalLine(r)
	if err != nil {
		return err
	}
	msg := s.format(line[:len(line)-1]) // strip trailing newline

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if err := s.write(msg); err != nil {
		// One redial covers a restarted daemon / recycled connection.
		if rerr := s.redial(); rerr == nil {
			err = s.write(msg)
		}
		if err != nil {
			metricsShared().DroppedTotal.WithLabelValues(s.Name(), "write_error").Inc()
			metricsShared().WriteErrors.WithLabelValues(s.Name()).Inc()
			if !s.failing {
				s.failing = true
				s.opts.Logger.Warn("audit: syslog delivery failing; records are dropped for this sink",
					slog.String("error", err.Error()))
			}
			return nil
		}
	}
	if s.failing {
		s.failing = false
		s.opts.Logger.Info("audit: syslog delivery recovered")
	}
	return nil
}

// Flush implements Sink. Messages are written per record; nothing buffers.
func (s *SyslogSink) Flush(context.Context) error { return nil }

// Close implements Sink.
func (s *SyslogSink) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// format renders the RFC 5424 message, framed for stream transports.
func (s *SyslogSink) format(body []byte) []byte {
	ts := s.opts.Clock().UTC().Format(time.RFC3339Nano)
	msg := fmt.Sprintf("<%d>1 %s %s %s %d - - %s",
		s.pri, ts, s.opts.Hostname, s.opts.Tag, os.Getpid(), body)
	if s.stream {
		// RFC 6587 octet counting: "LEN SP MSG".
		return []byte(strconv.Itoa(len(msg)) + " " + msg)
	}
	return []byte(msg)
}

func (s *SyslogSink) write(msg []byte) error {
	if s.conn == nil {
		return fmt.Errorf("audit: syslog connection is down")
	}
	_ = s.conn.SetWriteDeadline(s.opts.Clock().Add(s.opts.WriteTimeout))
	_, err := s.conn.Write(msg)
	return err
}

func (s *SyslogSink) redial() error {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	conn, err := net.DialTimeout(s.opts.Network, s.opts.Address, s.opts.DialTimeout)
	if err != nil {
		return err
	}
	s.conn = conn
	return nil
}
