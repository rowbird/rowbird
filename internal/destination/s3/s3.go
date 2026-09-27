// Package s3 delivers a run's files to an S3-compatible bucket, at paths built from a template
// such as reports/{{report.slug}}/{{run.date}}/{{report.slug}}.{{format}} (docs/spec/04-plugins.md).
package s3

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"mime"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"

	"github.com/rowbird/rowbird/internal/destination"
	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/storage"
	s3store "github.com/rowbird/rowbird/internal/storage/s3"
)

// ID is the plugin id.
const ID = "s3"

// DefaultPath is where files go unless the delivery says otherwise.
const DefaultPath = "reports/{{report.slug}}/{{run.date}}/{{report.slug}}.{{format}}"

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MergeMessages(destination.Messages(), plugin.MustLoadMessages(locales))

func init() {
	plugin.Register(plugin.KindDestination, ID, func() plugin.Plugin { return dest{} })
}

type dest struct{}

func (dest) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.s3.name", Description: "plugin.s3.description", Icon: "bucket", Version: "1.0.0"}
}

func (dest) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "endpoint", Type: plugin.TypeString, Required: true, Default: "https://s3.amazonaws.com", Format: "url", MaxLength: 500, Label: "plugin.s3.endpoint.label", Help: "plugin.s3.endpoint.help"},
		{Key: "region", Type: plugin.TypeString, Default: "us-east-1", MaxLength: 50, Label: "plugin.s3.region.label"},
		{Key: "bucket", Type: plugin.TypeString, Required: true, MaxLength: 63, Label: "plugin.s3.bucket.label"},
		{Key: "access_key", Type: plugin.TypeString, Required: true, Secret: true, MaxLength: 200, Label: "plugin.s3.access_key.label"},
		{Key: "secret_key", Type: plugin.TypeString, Required: true, Secret: true, MaxLength: 200, Label: "plugin.s3.secret_key.label"},
		{Key: "path_style", Type: plugin.TypeBoolean, Default: false, Group: "advanced", Label: "plugin.s3.path_style.label", Help: "plugin.s3.path_style.help"},
	}}
}

func (dest) DeliverySchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "path", Type: plugin.TypeString, Default: DefaultPath, MaxLength: 500, Label: "plugin.s3.path.label", Help: "plugin.s3.path.help"},
		{Key: "content_disposition", Type: plugin.TypeString, Default: "attachment", Enum: []string{"attachment", "inline"}, Label: "plugin.s3.content_disposition.label"},
	}}
}

func (dest) Capabilities() any {
	return plugin.DestinationCapabilities{SupportsAttachments: true, MaxAttachmentBytes: 5 << 30, Modes: []string{plugin.ModeAttachment}}
}

func (dest) Messages() plugin.Messages { return messages }

func client(env plugin.DestinationEnv) (*minio.Client, string, error) {
	get := func(k string) string { s, _ := env.Config[k].(string); return s }
	pathStyle, _ := env.Config["path_style"].(bool)
	cfg := s3store.Config{
		Endpoint: get("endpoint"), Region: get("region"), Bucket: get("bucket"),
		AccessKey: security.Secret(get("access_key")), SecretKey: security.Secret(get("secret_key")), PathStyle: pathStyle,
		Transport: env.HTTP.Transport, MaxRetries: 1,
	}
	c, err := s3store.Client(cfg)
	if err != nil {
		return nil, "", destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	}
	return c, cfg.Bucket, nil
}

// Key renders the object key of one file.
func Key(env plugin.DestinationEnv, msg plugin.Message, a plugin.Attachment) (string, error) {
	vars := destination.Vars(msg)
	vars.Format = strings.TrimPrefix(path.Ext(a.Name), ".")
	if vars.Format == "" {
		vars.Format = a.Format
	}
	src := format.String(env.Options, "path", DefaultPath)
	key, err := msgtemplate.Render(src, vars, msgtemplate.None)
	if err != nil {
		return "", err
	}
	key = strings.TrimLeft(path.Clean("/"+strings.TrimSpace(key)), "/")
	if storage.CheckKey(key) != nil {
		return "", destination.Err(plugin.ErrCodeDeliveryRejected, false, fmt.Errorf("s3: invalid path %q", key))
	}
	return key, nil
}

func (d dest) Send(ctx context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.SendResult, error) {
	if msg.Alert != nil {
		return plugin.SendResult{}, destination.Err(plugin.ErrCodeDeliveryRejected, false, errors.New("s3: a bucket cannot receive alerts"))
	}
	c, bucket, err := client(env)
	if err != nil {
		return plugin.SendResult{}, err
	}
	disposition := format.String(env.Options, "content_disposition", "attachment")
	var keys []string
	for _, a := range msg.Attachments {
		key, err := Key(env, msg, a)
		if err != nil {
			return plugin.SendResult{Meta: map[string]any{"keys": keys}}, err
		}
		r, err := a.Open()
		if err != nil {
			return plugin.SendResult{}, err
		}
		_, err = c.PutObject(ctx, bucket, key, r, a.Size, minio.PutObjectOptions{
			ContentType: a.ContentType, ContentDisposition: mime.FormatMediaType(disposition, map[string]string{"filename": a.Name}),
		})
		_ = r.Close()
		if err != nil {
			return plugin.SendResult{Meta: map[string]any{"keys": keys}}, mapError(err)
		}
		keys = append(keys, key)
	}
	return plugin.SendResult{Meta: map[string]any{"bucket": bucket, "keys": keys}}, nil
}

func mapError(err error) error {
	r := minio.ToErrorResponse(err)
	switch r.Code {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken", "InvalidToken":
		return destination.Err(plugin.ErrCodeDeliveryAuth, false, err)
	case "NoSuchBucket", "InvalidBucketName", "EntityTooLarge", "InvalidArgument":
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, err)
	case "SlowDown", "RequestTimeout", "InternalError", "ServiceUnavailable":
		return destination.Err(plugin.ErrCodeDeliveryFailed, true, err)
	}
	if r.StatusCode != 0 {
		return destination.StatusError(r.StatusCode, r.Code)
	}
	return destination.NetworkError(err)
}

// Test checks that the bucket exists and the keys can see it.
func (d dest) Test(ctx context.Context, env plugin.DestinationEnv) error {
	c, bucket, err := client(env)
	if err != nil {
		return err
	}
	ok, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return mapError(err)
	}
	if !ok {
		return destination.Err(plugin.ErrCodeDeliveryRejected, false, errors.New("s3: the bucket does not exist"))
	}
	return nil
}

func (d dest) Preview(_ context.Context, env plugin.DestinationEnv, msg plugin.Message) (plugin.Preview, error) {
	bucket, _ := env.Config["bucket"].(string)
	var lines []string
	for _, a := range msg.Attachments {
		key, err := Key(env, msg, a)
		if err != nil {
			return plugin.Preview{}, err
		}
		lines = append(lines, "s3://"+bucket+"/"+key)
	}
	return plugin.Preview{Body: strings.Join(lines, "\n"), BodyType: "text"}, nil
}
