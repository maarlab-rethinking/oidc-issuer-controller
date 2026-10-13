/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package store writes the issuer documents to an S3-compatible bucket. The controller
// only talks to the Store interface, so its logic is tested against an in-memory fake; the
// aws-sdk-go-v2 implementation is exercised against a real S3 server in the integration
// tests.
package store

import (
	"context"
	"errors"
	"fmt"
)

// Config is what a Store needs to reach one bucket with one credential.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

// Store reads and writes objects in one bucket.
type Store interface {
	// Get returns an object's content, or ErrNotFound.
	Get(ctx context.Context, key string) ([]byte, error)
	// Put writes an object as JSON with the given Cache-Control.
	Put(ctx context.Context, key string, body []byte, cacheControl string) error
}

// NewStoreFunc builds a Store.
type NewStoreFunc func(cfg Config) (Store, error)

// ErrNotFound is returned by Get for a missing object.
var ErrNotFound = errors.New("object not found")

// CredentialsError means the store rejected the key pair itself.
type CredentialsError struct {
	Code, Message string
}

func (e *CredentialsError) Error() string {
	return fmt.Sprintf("credentials rejected: %s: %s", e.Code, e.Message)
}

// IsCredentialsError reports whether err is (or wraps) a CredentialsError.
func IsCredentialsError(err error) bool {
	var ce *CredentialsError
	return errors.As(err, &ce)
}
