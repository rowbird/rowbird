// Package json is the JSON formatter: an array of objects or newline-delimited JSON, with keys in
// column order. Exact numbers stay exact: decimals are strings by default and integers beyond
// what a JavaScript number holds are strings too (docs/spec/04-plugins.md).
package json

import (
	"bytes"
	"context"
	"embed"
	stdjson "encoding/json"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/plugin"
)

// ID is the plugin id.
const ID = "json"

//go:embed locales/*.json
var locales embed.FS

var messages = plugin.MustLoadMessages(locales)

func init() {
	plugin.Register(plugin.KindFormatter, ID, func() plugin.Plugin { return formatter{} })
}

type formatter struct{}

func (formatter) Meta() plugin.Metadata {
	return plugin.Metadata{ID: ID, Name: "plugin.format.json.name", Description: "plugin.format.json.description", Icon: "json", Version: "1.0.0"}
}

func (formatter) ConfigSchema() *plugin.Schema {
	return &plugin.Schema{Fields: []plugin.Field{
		{Key: "layout", Type: plugin.TypeString, Default: "array", Enum: []string{"array", "ndjson"}, Label: "plugin.format.json.layout.label"},
		{Key: "decimals", Type: plugin.TypeString, Default: "string", Enum: []string{"string", "number"}, Label: "plugin.format.json.decimals.label", Help: "plugin.format.json.decimals.help"},
	}}
}

func (f formatter) Capabilities() any {
	return plugin.FormatterCapabilities{Kind: plugin.FormatterFile, ContentType: "application/json", Extension: "json"}
}

func (formatter) Messages() plugin.Messages { return messages }

func (formatter) Format(ctx context.Context, in plugin.FormatInput, w io.Writer) (plugin.FormatResult, error) {
	var res plugin.FormatResult
	if err := ctx.Err(); err != nil {
		return res, err
	}
	ndjson := format.String(in.Options, "layout", "array") == "ndjson"
	numbers := format.String(in.Options, "decimals", "string") == "number"
	keys := make([][]byte, len(in.Columns))
	for i, c := range in.Columns {
		k, err := stdjson.Marshal(c.Name)
		if err != nil {
			return res, err
		}
		keys[i] = k
	}
	var buf bytes.Buffer
	if !ndjson {
		buf.WriteString("[")
	}
	for in.Rows.Next() {
		if res.Rows%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return res, err
			}
		}
		switch {
		case ndjson:
		case res.Rows == 0:
			buf.WriteString("\n")
		default:
			buf.WriteString(",\n")
		}
		row := in.Rows.Row()
		buf.WriteByte('{')
		for i, c := range in.Columns {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(keys[i])
			buf.WriteByte(':')
			var v any
			if i < len(row) {
				v = row[i]
			}
			if err := value(&buf, c, v, numbers, in.Location); err != nil {
				return res, err
			}
		}
		buf.WriteByte('}')
		if ndjson {
			buf.WriteByte('\n')
		}
		res.Rows++
		if buf.Len() > 64<<10 {
			if _, err := w.Write(buf.Bytes()); err != nil {
				return res, err
			}
			buf.Reset()
		}
	}
	if err := in.Rows.Err(); err != nil {
		return res, err
	}
	if !ndjson {
		if res.Rows > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("]\n")
	}
	_, err := w.Write(buf.Bytes())
	return res, err
}

func value(buf *bytes.Buffer, col plugin.Column, v any, numbers bool, loc *time.Location) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case int64:
		if x > plugin.MaxSafeInteger || x < -plugin.MaxSafeInteger {
			return str(buf, strconv.FormatInt(x, 10))
		}
		buf.WriteString(strconv.FormatInt(x, 10))
		return nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return str(buf, strconv.FormatFloat(x, 'g', -1, 64))
		}
		buf.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
		return nil
	case plugin.Decimal:
		if numbers && stdjson.Valid([]byte(x)) {
			buf.WriteString(string(x))
			return nil
		}
		return str(buf, string(x))
	case bool:
		buf.WriteString(strconv.FormatBool(x))
		return nil
	case plugin.JSON:
		if stdjson.Valid([]byte(x)) {
			return stdjson.Compact(buf, []byte(x))
		}
		return str(buf, string(x))
	case time.Time:
		t := format.Instant(col, x, loc)
		if col.WithTimeZone {
			return str(buf, t.Format(time.RFC3339Nano))
		}
		return str(buf, t.Format("2006-01-02T15:04:05.999999999"))
	}
	return marshal(buf, plugin.JSONValue(v)) // string, date, time of day, binary (base64)
}

func str(buf *bytes.Buffer, s string) error { return marshal(buf, s) }

func marshal(buf *bytes.Buffer, v any) error {
	b, err := stdjson.Marshal(v)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}
