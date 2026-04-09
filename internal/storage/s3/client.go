package s3

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// Client 只读 S3 封装。
type Client struct {
	api *awss3.Client
}

// New 使用默认凭证链；cfg.Endpoint 非空时使用 custom endpoint（path-style 兼容 MinIO）。
func New(ctx context.Context, cfg Config) (*Client, error) {
	opts := []func(*config.LoadOptions) error{}
	if cfg.Region != "" {
		opts = append(opts, config.WithRegion(cfg.Region))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3 aws config: %w", err)
	}
	s3opts := []func(*awss3.Options){}
	if cfg.Endpoint != "" {
		s3opts = append(s3opts, func(o *awss3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	}
	return &Client{api: awss3.NewFromConfig(awsCfg, s3opts...)}, nil
}

// GetObjectBody 返回对象流，调用方负责 Close。
func (c *Client) GetObjectBody(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("s3: nil client")
	}
	bucket = strings.TrimSpace(bucket)
	key = strings.TrimSpace(key)
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("s3: empty bucket or key")
	}
	out, err := c.api.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

// HeadObject 是否存在及 ContentLength。
func (c *Client) HeadObject(ctx context.Context, bucket, key string) (size int64, ok bool, err error) {
	if c == nil || c.api == nil {
		return 0, false, fmt.Errorf("s3: nil client")
	}
	out, err := c.api.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(strings.TrimSpace(bucket)),
		Key:    aws.String(strings.TrimSpace(key)),
	})
	if err != nil {
		return 0, false, err
	}
	var n int64
	if out.ContentLength != nil {
		n = *out.ContentLength
	}
	return n, true, nil
}

// ListObjectKeys 列举 prefix 下全部 key（分页）。
func (c *Client) ListObjectKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("s3: nil client")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, fmt.Errorf("s3: empty bucket")
	}
	prefix = strings.TrimSpace(prefix)
	var keys []string
	paginator := awss3.NewListObjectsV2Paginator(c.api, &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			if o.Key != nil && *o.Key != "" {
				keys = append(keys, *o.Key)
			}
		}
	}
	return keys, nil
}

// ParseS3URI 解析 s3://bucket/key 形式；key 可含 /。
func ParseS3URI(uri string) (bucket, key string, ok bool) {
	uri = strings.TrimSpace(uri)
	const p = "s3://"
	if !strings.HasPrefix(strings.ToLower(uri), p) {
		return "", "", false
	}
	rest := uri[len(p):]
	i := strings.Index(rest, "/")
	if i <= 0 || i >= len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}
