package schedule

import (
	"strings"

	crondesc "github.com/lnquy/cron"
)

var describer = func() *crondesc.ExpressionDescriptor {
	d, err := crondesc.NewDescriptor(
		crondesc.Use24HourTimeFormat(true),
		crondesc.SetLocales(crondesc.Locale_en, crondesc.Locale_pt_BR),
	)
	if err != nil {
		panic(err)
	}
	return d
}()

// Describe returns a human description of a valid expression in locale ("en" or "pt-BR"; other
// locales fall back to English). It returns the empty string when the expression cannot be
// described, and callers show the expression itself instead.
func Describe(expr, locale string) string {
	expr = strings.Join(strings.Fields(expr), " ")
	if five, ok := descriptors[strings.ToLower(expr)]; ok {
		expr = five
	}
	loc := crondesc.Locale_en
	if strings.EqualFold(locale, "pt-BR") || strings.EqualFold(locale, "pt_BR") {
		loc = crondesc.Locale_pt_BR
	}
	out, err := describer.ToDescription(expr, loc)
	if err != nil {
		return ""
	}
	return out
}
