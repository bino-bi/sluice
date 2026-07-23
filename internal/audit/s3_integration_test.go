// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/bino-bi/sluice/internal/audit"
)

// TestS3Sink_RealClientAgainstStub drives a real *minio.Client (SigV4
// signing, path-style addressing) against an httptest S3 stub, proving the
// production wire path: the object body is the concatenated MarshalLine
// output and the Object Lock mode/retention arrive as x-amz headers.
func TestS3Sink_RealClientAgainstStub(t *testing.T) {
	type putReq struct {
		path    string
		body    []byte
		headers http.Header
	}
	var (
		mu   sync.Mutex
		puts []putReq
	)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") ||
			strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
			body = decodeAWSChunked(t, body)
		}
		mu.Lock()
		puts = append(puts, putReq{path: r.URL.Path, body: body, headers: r.Header.Clone()})
		mu.Unlock()
		w.Header().Set("ETag", `"stub-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()

	client, err := minio.New(strings.TrimPrefix(stub.URL, "http://"), &minio.Options{
		Creds:        credentials.NewStaticV4("test-access", "test-secret", ""),
		Secure:       false,
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatalf("minio.New: %v", err)
	}

	sink, err := audit.NewS3Sink(audit.S3Options{
		Store:         client,
		Bucket:        "audit-bucket",
		Prefix:        "audit/",
		ObjectLock:    "governance",
		RetentionDays: 30,
		Logger:        testLogger(t),
	})
	if err != nil {
		t.Fatalf("NewS3Sink: %v", err)
	}

	recs := []*audit.Record{
		{EventType: audit.EventQuery, Decision: audit.DecisionAllow, Hash: "h1"},
		{EventType: audit.EventQuery, Decision: audit.DecisionDeny, Hash: "h2"},
	}
	for _, r := range recs {
		if err := sink.Record(context.Background(), r); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 1 {
		t.Fatalf("PUT requests = %d, want 1", len(puts))
	}
	put := puts[0]
	if !strings.HasPrefix(put.path, "/audit-bucket/audit/") || !strings.HasSuffix(put.path, ".jsonl") {
		t.Fatalf("object path = %q, want /audit-bucket/audit/…/audit-<ulid>.jsonl", put.path)
	}
	var wantBody []byte
	for _, r := range recs {
		line, err := audit.MarshalLine(r)
		if err != nil {
			t.Fatalf("MarshalLine: %v", err)
		}
		wantBody = append(wantBody, line...)
	}
	if string(put.body) != string(wantBody) {
		t.Fatalf("body mismatch:\ngot  %q\nwant %q", put.body, wantBody)
	}
	if got := put.headers.Get("X-Amz-Object-Lock-Mode"); got != "GOVERNANCE" {
		t.Fatalf("X-Amz-Object-Lock-Mode = %q, want GOVERNANCE", got)
	}
	if got := put.headers.Get("X-Amz-Object-Lock-Retain-Until-Date"); got == "" {
		t.Fatal("X-Amz-Object-Lock-Retain-Until-Date header missing")
	}
}

// decodeAWSChunked strips the SigV4 streaming-payload framing
// ("<hex-len>;chunk-signature=…\r\n<payload>\r\n…", terminated by a
// zero-length chunk) and returns the raw payload.
func decodeAWSChunked(t *testing.T, body []byte) []byte {
	t.Helper()
	var out []byte
	rest := body
	for {
		nl := strings.Index(string(rest), "\r\n")
		if nl < 0 {
			t.Fatalf("aws-chunked: missing chunk header terminator in %q", rest)
		}
		header := string(rest[:nl])
		sizeHex, _, _ := strings.Cut(header, ";")
		var size int64
		if _, err := fmt.Sscanf(sizeHex, "%x", &size); err != nil {
			t.Fatalf("aws-chunked: bad chunk size %q: %v", sizeHex, err)
		}
		rest = rest[nl+2:]
		if size == 0 {
			return out
		}
		if int64(len(rest)) < size+2 {
			t.Fatalf("aws-chunked: truncated chunk (want %d bytes, have %d)", size, len(rest))
		}
		out = append(out, rest[:size]...)
		rest = rest[size+2:]
	}
}
