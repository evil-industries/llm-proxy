package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7/pkg/s3utils"
)

// LogArchiveConfig configures permanent, compressed storage of completed logs.
// The bucket must already exist and must not expire archived objects.
type LogArchiveConfig struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	Endpoint        string `yaml:"endpoint" json:"endpoint"`
	Bucket          string `yaml:"bucket" json:"bucket"`
	Region          string `yaml:"region" json:"region"`
	Prefix          string `yaml:"prefix" json:"prefix"`
	AccessKeyID     string `yaml:"access-key-id" json:"-"`
	SecretAccessKey string `yaml:"secret-access-key" json:"-"`
	SessionToken    string `yaml:"session-token" json:"-"`
	PathStyle       bool   `yaml:"path-style" json:"path-style"`
}

// WithDefaults returns normalized settings without resolving credentials or making network calls.
func (c LogArchiveConfig) WithDefaults() LogArchiveConfig {
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	if c.Endpoint == "" {
		c.Endpoint = "https://s3.amazonaws.com"
	}
	if !strings.Contains(c.Endpoint, "://") {
		c.Endpoint = "https://" + c.Endpoint
	}
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.Region = strings.TrimSpace(c.Region)
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	if c.Prefix == "" {
		c.Prefix = "logs"
	}
	return c
}

// Validate checks archive configuration without contacting the storage provider.
func (c LogArchiveConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	c = c.WithDefaults()
	if errBucket := s3utils.CheckValidBucketName(c.Bucket); errBucket != nil {
		return fmt.Errorf("log-archive.bucket must be a valid S3 bucket name")
	}
	endpoint, errParse := url.Parse(c.Endpoint)
	if errParse != nil || endpoint.Host == "" || endpoint.Hostname() == "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return fmt.Errorf("log-archive.endpoint must be an HTTP(S) host without credentials, path, query, or fragment")
	}
	if (c.AccessKeyID == "") != (c.SecretAccessKey == "") || (c.SessionToken != "" && c.AccessKeyID == "") {
		return fmt.Errorf("log-archive requires both access-key-id and secret-access-key when static credentials are provided")
	}
	for _, part := range strings.Split(c.Prefix, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00\r\n") {
			return fmt.Errorf("log-archive.prefix contains an invalid path component")
		}
	}
	return nil
}
