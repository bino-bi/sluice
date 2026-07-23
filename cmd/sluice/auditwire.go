// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/secrets"
)

// buildAuditSinks constructs the audit sink chain: the file sink first
// (chain primary — it carries the hash chain and seeds it across
// restarts), then the optional best-effort syslog and s3 sinks. A missing
// file config falls back to a temp directory with a loud warning so the
// server still boots.
func buildAuditSinks(ctx context.Context, scfg *config.ServerConfig, resolver *secrets.Resolver, log *slog.Logger) ([]audit.Sink, error) {
	var sinks []audit.Sink

	if scfg.Audit.File != nil && scfg.Audit.File.Path != "" {
		sink, err := audit.NewFileSink(audit.FileOptions{
			Dir:          scfg.Audit.File.Path,
			RotateDaily:  scfg.Audit.File.RotateDaily,
			RotateSizeMB: scfg.Audit.File.RotateSizeMB,
		})
		if err != nil {
			return nil, fmt.Errorf("audit file sink: %w", err)
		}
		sinks = append(sinks, sink)
	} else {
		tmp := filepath.Join(os.TempDir(), "sluice-audit")
		log.Warn("audit: no sink configured; defaulting to temp directory", slog.String("path", tmp))
		sink, err := audit.NewFileSink(audit.FileOptions{Dir: tmp, RotateDaily: true})
		if err != nil {
			return nil, fmt.Errorf("audit fallback sink: %w", err)
		}
		sinks = append(sinks, sink)
	}

	if sc := scfg.Audit.Syslog; sc != nil {
		sink, err := audit.NewSyslogSink(audit.SyslogOptions{
			Network:  sc.Network,
			Address:  sc.Address,
			Facility: sc.Facility,
			Tag:      sc.Tag,
			Logger:   log,
		})
		if err != nil {
			return nil, fmt.Errorf("audit syslog sink: %w", err)
		}
		sinks = append(sinks, sink)
	}

	if sc := scfg.Audit.S3; sc != nil {
		client, err := buildS3Client(ctx, sc, resolver)
		if err != nil {
			return nil, fmt.Errorf("audit s3 sink: %w", err)
		}
		sink, err := audit.NewS3Sink(audit.S3Options{
			Store:          client,
			Bucket:         sc.Bucket,
			Prefix:         sc.Prefix,
			ObjectLock:     sc.ObjectLock,
			RetentionDays:  sc.RetentionDays,
			UploadInterval: sc.UploadInterval,
			UploadBytes:    sc.UploadBytes,
			MaxBufferBytes: sc.MaxBufferBytes,
			Logger:         log,
		})
		if err != nil {
			return nil, fmt.Errorf("audit s3 sink: %w", err)
		}
		// Best-effort reachability probe: an unreachable bucket must not
		// abort boot (records buffer and the metrics/logs make it
		// visible), but the operator should hear about it immediately.
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if ok, err := client.BucketExists(probeCtx, sc.Bucket); err != nil {
			log.Warn("audit: s3 bucket probe failed; records will buffer until the sink recovers",
				slog.String("bucket", sc.Bucket), slog.String("error", err.Error()))
		} else if !ok {
			log.Warn("audit: s3 bucket does not exist; records will buffer until it does",
				slog.String("bucket", sc.Bucket))
		}
		cancel()
		sinks = append(sinks, sink)
	}

	return sinks, nil
}

// buildS3Client assembles the minio client. Credentials come from
// audit.s3.credentialsRef (a secret:// reference to a JSON object with
// accessKeyId / secretAccessKey / sessionToken) or, when unset, from the
// standard env / shared-file / IAM chain so IRSA and instance profiles
// work with zero config.
func buildS3Client(ctx context.Context, sc *config.S3SinkConfig, resolver *secrets.Resolver) (*minio.Client, error) {
	endpoint := sc.Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}

	var creds *credentials.Credentials
	if sc.CredentialsRef != "" {
		raw, err := resolver.Resolve(ctx, sc.CredentialsRef)
		if err != nil {
			return nil, fmt.Errorf("resolve credentialsRef: %w", err)
		}
		var parsed struct {
			AccessKeyID     string `json:"accessKeyId"`
			SecretAccessKey string `json:"secretAccessKey"`
			SessionToken    string `json:"sessionToken"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("credentialsRef %q: expected a JSON object with accessKeyId/secretAccessKey: %w", sc.CredentialsRef, err)
		}
		if parsed.AccessKeyID == "" || parsed.SecretAccessKey == "" {
			return nil, fmt.Errorf("credentialsRef %q: accessKeyId and secretAccessKey are required", sc.CredentialsRef)
		}
		creds = credentials.NewStaticV4(parsed.AccessKeyID, parsed.SecretAccessKey, parsed.SessionToken)
	} else {
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.EnvMinio{},
			&credentials.FileAWSCredentials{},
			&credentials.IAM{},
		})
	}

	opts := &minio.Options{
		Creds:  creds,
		Secure: !sc.Insecure,
		Region: sc.Region,
	}
	if sc.ForcePathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	return minio.New(endpoint, opts)
}
