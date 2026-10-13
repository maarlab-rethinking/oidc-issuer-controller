/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// httpTimeout bounds every single S3 request; the reconcile context bounds the sum.
const httpTimeout = 30 * time.Second

// maxObjectSize caps what Get reads back. The documents are a few KiB.
const maxObjectSize = 1 << 20

// Error codes that mean the key pair itself is wrong or not allowed on the bucket.
var credentialErrorCodes = map[string]bool{
	"InvalidAccessKeyId":    true,
	"SignatureDoesNotMatch": true,
	"InvalidToken":          true,
	"ExpiredToken":          true,
	"AccessDenied":          true,
	"Unauthorized":          true,
}

type s3Store struct {
	api    *s3.Client
	bucket string
}

// NewS3Store returns a Store backed by aws-sdk-go-v2.
func NewS3Store(cfg Config) (Store, error) {
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.Bucket == "" {
		return nil, errors.New("endpoint, region and bucket are required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("access key ID and secret access key are required")
	}
	api := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle: cfg.ForcePathStyle,
		HTTPClient:   awshttp.NewBuildableClient().WithTimeout(httpTimeout),
		// Since 2025 the SDK adds CRC32 checksums to every request that supports them,
		// which several S3-compatible services reject. Only send them where the S3 API
		// requires one.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		AppID:                      "oidc-issuer-controller",
	})
	return &s3Store{api: api, bucket: cfg.Bucket}, nil
}

func classify(op string, err error) error {
	var ae smithy.APIError
	if errors.As(err, &ae) && credentialErrorCodes[ae.ErrorCode()] {
		return &CredentialsError{Code: ae.ErrorCode(), Message: ae.ErrorMessage()}
	}
	return fmt.Errorf("%s: %w", op, err)
}

func (s *s3Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.api.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var ae smithy.APIError
		if errors.As(err, &ae) && (ae.ErrorCode() == "NoSuchKey" || ae.ErrorCode() == "NotFound") {
			return nil, ErrNotFound
		}
		return nil, classify("GetObject "+key, err)
	}
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(out.Body, maxObjectSize))
	if err != nil {
		return nil, fmt.Errorf("GetObject %s: read body: %w", key, err)
	}
	return body, nil
}

func (s *s3Store) Put(ctx context.Context, key string, body []byte, cacheControl string) error {
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String("application/json"),
	}
	if cacheControl != "" {
		in.CacheControl = aws.String(cacheControl)
	}
	if _, err := s.api.PutObject(ctx, in); err != nil {
		return classify("PutObject "+key, err)
	}
	return nil
}
