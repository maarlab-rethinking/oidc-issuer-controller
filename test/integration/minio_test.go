//go:build integration

/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package integration runs the S3 store against a real S3 server, MinIO. It needs a
// Docker-compatible daemon (testcontainers): run it with `make test-integration`.
package integration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/store"
)

const (
	rootUser     = "integration"
	rootPassword = "integration-secret"
	region       = "auto"
	bucket       = "issuers"
)

// minioImage defaults to Chainguard's MinIO build: MinIO no longer publishes community
// images to Docker Hub or Quay. Override with MINIO_IMAGE.
func minioImage() string {
	if img := os.Getenv("MINIO_IMAGE"); img != "" {
		return img
	}
	return "cgr.dev/chainguard/minio:latest"
}

func startMinIO(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := testcontainers.Run(ctx, minioImage(),
		testcontainers.WithCmd("server", "/data"),
		testcontainers.WithEnv(map[string]string{
			"MINIO_ROOT_USER":     rootUser,
			"MINIO_ROOT_PASSWORD": rootPassword,
		}),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp").WithStartupTimeout(2*time.Minute)),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start MinIO: %v", err)
	}
	endpoint, err := ctr.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func newStore(t *testing.T, endpoint, sk string) store.Store {
	t.Helper()
	s, err := store.NewS3Store(store.Config{
		Endpoint: endpoint, Region: region, Bucket: bucket,
		AccessKeyID: rootUser, SecretAccessKey: sk, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// sdk is a raw client for the setup and the checks the Store interface does not expose.
func sdk(endpoint string) *s3.Client {
	return s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(rootUser, rootPassword, ""),
		UsePathStyle: true,
	})
}

func makeBucket(ctx context.Context, endpoint string) error {
	_, err := sdk(endpoint).CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	return err
}

func headers(ctx context.Context, endpoint, key string) (string, string, error) {
	out, err := sdk(endpoint).HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return "", "", err
	}
	return aws.ToString(out.ContentType), aws.ToString(out.CacheControl), nil
}

// TestS3Store checks the store against a real server: the checksum settings, the
// Content-Type and Cache-Control it writes, the not-found mapping and the
// credentials-error classification.
func TestS3Store(t *testing.T) {
	endpoint := startMinIO(t)
	ctx := context.Background()
	s := newStore(t, endpoint, rootPassword)
	if err := makeBucket(ctx, endpoint); err != nil {
		t.Fatal(err)
	}

	t.Run("missing object", func(t *testing.T) {
		if _, err := s.Get(ctx, "nope/.well-known/openid-configuration"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		const key = "cluster-a/openid/v1/jwks"
		body := []byte(`{"keys":[{"kid":"k1"}]}`)
		if err := s.Put(ctx, key, body, "max-age=300"); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, key)
		if err != nil || string(got) != string(body) {
			t.Fatalf("got %s, %v", got, err)
		}
		ct, cc, err := headers(ctx, endpoint, key)
		if err != nil || ct != "application/json" || cc != "max-age=300" {
			t.Fatalf("Content-Type %q, Cache-Control %q, %v", ct, cc, err)
		}
	})

	t.Run("rejected credentials", func(t *testing.T) {
		err := newStore(t, endpoint, "wrong").Put(ctx, "x", []byte("{}"), "")
		if !store.IsCredentialsError(err) {
			t.Fatalf("err = %v, want a CredentialsError", err)
		}
	})
}
