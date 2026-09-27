package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// Connector opens connections to a user database (docs/spec/04-plugins.md, "Connectors").
type Connector interface {
	Plugin
	Open(ctx context.Context, opts OpenOptions) (Conn, error)
}

// DialFunc dials a TCP address. The core passes one that enforces ROWBIRD_NETWORK_POLICY and, when
// configured, goes through an SSH tunnel. Network connectors must use it for every connection.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// OpenOptions carry the connection configuration and the environment a connector may use.
type OpenOptions struct {
	// Values holds validated configuration and decrypted secrets together.
	Values map[string]any
	Dial   DialFunc
	// SQLiteDirs are the directories file-based connectors may open (ROWBIRD_SQLITE_DIRS).
	SQLiteDirs []string
	// ForbiddenPaths may never be opened, whatever SQLiteDirs says (the internal store).
	ForbiddenPaths []string
	Logger         *slog.Logger
}

// Conn is an open connection. Implementations must be safe for sequential use by one caller.
type Conn interface {
	Ping(ctx context.Context) (ServerInfo, error)
	Schema(ctx context.Context) (*DBSchema, error)
	// CanWrite reports whether the user can modify data; nil means unknown.
	CanWrite(ctx context.Context) (*bool, error)
	Query(ctx context.Context, q BoundQuery, opts QueryOptions) (RowStream, error)
	Close() error
}

// ServerInfo describes the database server.
type ServerInfo struct {
	Version string `json:"version"`
}

// BoundQuery is SQL with its arguments bound as driver parameters, never interpolated.
type BoundQuery struct {
	SQL  string
	Args []any
}

// QueryOptions limit a query (docs/spec/07-security.md, "Protecting user databases").
type QueryOptions struct {
	Timeout             time.Duration
	MaxRows             int
	ReadOnly            bool
	AllowMultiStatement bool
}

// ConnectorCapabilities describe what a connector can guarantee.
type ConnectorCapabilities struct {
	// ReadOnlyTx: queries run in a transaction the database itself keeps read-only.
	ReadOnlyTx bool `json:"read_only_tx"`
	// ServerTimeout: the database enforces the timeout, not only the client.
	ServerTimeout bool `json:"server_timeout"`
	// Cancel: a running query stops on the server when the context is cancelled.
	Cancel bool `json:"cancel"`
	// MultiStatement: the connector can run several statements when a connection allows it.
	MultiStatement bool `json:"multi_statement"`
	// PlaceholderStyle: "dollar" ($1), "question" (?) or "at" (@p1).
	PlaceholderStyle string `json:"placeholder_style"`
	// Dialect for the SQL editor and the AI assistant.
	Dialect string `json:"dialect"`
	// Network: the connector reaches a server over TCP (TLS and SSH options apply).
	Network bool `json:"network"`
}

// ValueType is a normalized column type.
type ValueType string

// Normalized types. Values in RowStream rows use the Go types noted.
const (
	TypeText     ValueType = "text"     // string
	TypeInt      ValueType = "integer"  // int64
	TypeDecimal  ValueType = "decimal"  // Decimal (exact, string backed)
	TypeFloat    ValueType = "float"    // float64
	TypeBool     ValueType = "boolean"  // bool
	TypeDate     ValueType = "date"     // Date
	TypeDateTime ValueType = "datetime" // time.Time (see Column.WithTimeZone)
	TypeTime     ValueType = "time"     // TimeOfDay
	TypeJSON     ValueType = "json"     // JSON
	TypeBinary   ValueType = "binary"   // []byte
	TypeUnknown  ValueType = "unknown"  // string
)

// Decimal is an exact decimal number in canonical text form ("-12.3400"). It is never converted to
// float (AGENTS.md, "Money/decimals are never converted to float").
type Decimal string

// Date is a calendar date, "2006-01-02".
type Date string

// TimeOfDay is a time without a date, "15:04:05" with optional fractional seconds.
type TimeOfDay string

// JSON is a JSON document as returned by the database.
type JSON string

// Column describes a result column.
type Column struct {
	Name   string    `json:"name"`
	Type   ValueType `json:"type"`
	DBType string    `json:"db_type"`
	// WithTimeZone is true for datetime columns that carry a zone (timestamptz, datetimeoffset).
	WithTimeZone bool `json:"with_time_zone,omitempty"`
}

// RowStream iterates over result rows. Row values follow ValueType; NULL is nil.
type RowStream interface {
	Columns() []Column
	Next() bool
	Row() []any
	Err() error
	// Truncated is true when rows beyond QueryOptions.MaxRows were dropped.
	Truncated() bool
	Close() error
}

// DBSchema is the introspected structure of a database.
type DBSchema struct {
	Tables []Table `json:"tables"`
}

// Table is a table or view.
type Table struct {
	Schema  string        `json:"schema,omitempty"`
	Name    string        `json:"name"`
	Kind    string        `json:"kind"` // table or view
	Comment string        `json:"comment,omitempty"`
	Columns []TableColumn `json:"columns"`
}

// QualifiedName is "schema.name", or "name" without a schema.
func (t Table) QualifiedName() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// TableColumn is a column of a table.
type TableColumn struct {
	Name     string    `json:"name"`
	Type     ValueType `json:"type"`
	DBType   string    `json:"db_type"`
	Nullable bool      `json:"nullable"`
	Comment  string    `json:"comment,omitempty"`
}

// Connection error codes (docs/spec/03-flows.md, "Create a connection").
const (
	ErrCodeAuthFailed        = "connection.auth_failed"
	ErrCodeHostUnreachable   = "connection.host_unreachable"
	ErrCodeTLSRequired       = "connection.tls_required"
	ErrCodeTLSFailed         = "connection.tls_failed"
	ErrCodeDatabaseNotFound  = "connection.database_not_found"
	ErrCodeTimeout           = "connection.timeout"
	ErrCodeNetworkBlocked    = "connection.network_blocked"
	ErrCodePathNotAllowed    = "connection.path_not_allowed"
	ErrCodeSSHAuthFailed     = "connection.ssh_auth_failed"
	ErrCodeSSHHostKeyUnknown = "connection.ssh_host_key_unknown"
	ErrCodeSSHHostKeyChanged = "connection.ssh_host_key_mismatch"
	ErrCodeSSHUnreachable    = "connection.ssh_unreachable"
	ErrCodeFailed            = "connection.failed"
	// Query errors.
	ErrCodeQueryTimeout      = "query.timeout"
	ErrCodeQueryCancelled    = "query.cancelled"
	ErrCodeMultiStatement    = "query.multiple_statements"
	ErrCodeReadOnlyViolation = "query.read_only"
	ErrCodeQueryFailed       = "query.failed"
)

// ConnError is a failure with a stable code. Detail holds safe data for the user (for example the
// SSH fingerprint to confirm); Err keeps the driver error for logs and must never reach responses.
type ConnError struct {
	Code   string
	Detail map[string]string
	Err    error
}

func (e *ConnError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Code
}

func (e *ConnError) Unwrap() error { return e.Err }

// NewConnError wraps err with a code.
func NewConnError(code string, err error) *ConnError { return &ConnError{Code: code, Err: err} }

// AsConnError extracts a ConnError.
func AsConnError(err error) (*ConnError, bool) {
	var ce *ConnError
	ok := errors.As(err, &ce)
	return ce, ok
}
