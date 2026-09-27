package app

// Compiled-in plugins (ADR-0004). Each package registers itself in init.
import (
	_ "github.com/rowbird/rowbird/internal/ai/anthropic"
	_ "github.com/rowbird/rowbird/internal/ai/gemini"
	_ "github.com/rowbird/rowbird/internal/ai/ollama"
	_ "github.com/rowbird/rowbird/internal/ai/openai"
	_ "github.com/rowbird/rowbird/internal/condition"
	_ "github.com/rowbird/rowbird/internal/connector/mssql"
	_ "github.com/rowbird/rowbird/internal/connector/mysql"
	_ "github.com/rowbird/rowbird/internal/connector/postgres"
	_ "github.com/rowbird/rowbird/internal/connector/sqlite"
	_ "github.com/rowbird/rowbird/internal/destination/discord"
	_ "github.com/rowbird/rowbird/internal/destination/email"
	_ "github.com/rowbird/rowbird/internal/destination/s3"
	_ "github.com/rowbird/rowbird/internal/destination/slack"
	_ "github.com/rowbird/rowbird/internal/destination/telegram"
	_ "github.com/rowbird/rowbird/internal/destination/uptimekuma"
	_ "github.com/rowbird/rowbird/internal/destination/webhook"
	_ "github.com/rowbird/rowbird/internal/format/csv"
	_ "github.com/rowbird/rowbird/internal/format/inline"
	_ "github.com/rowbird/rowbird/internal/format/json"
	_ "github.com/rowbird/rowbird/internal/format/pdf"
	_ "github.com/rowbird/rowbird/internal/format/xlsx"
)
